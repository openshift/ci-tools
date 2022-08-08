package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
)

func CreateOrRestartPod(ctx context.Context, podClient ctrlruntimeclient.Client, pod *corev1.Pod) (*corev1.Pod, error) {
	namespace, name := pod.Namespace, pod.Name
	if err := waitForCompletedPodDeletion(ctx, podClient, namespace, name); err != nil {
		return nil, fmt.Errorf("unable to delete completed pod: %w", err)
	}
	if pod.Spec.ActiveDeadlineSeconds == nil {
		logrus.Debugf("Executing pod %q running image %q", pod.Name, pod.Spec.Containers[0].Image)
	} else {
		logrus.Debugf("Executing pod %q with activeDeadlineSeconds=%d", pod.Name, *pod.Spec.ActiveDeadlineSeconds)
	}
	// creating a pod in close proximity to namespace creation can result in forbidden errors due to
	// initializing secrets or policy - use a short backoff to mitigate flakes
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Factor: 2, Duration: time.Second}, func() (bool, error) {
		err := podClient.Create(ctx, pod)
		if err != nil {
			if kerrors.IsForbidden(err) {
				logrus.WithError(err).Warnf("Unable to create pod %s, may be temporary.", name)
				return false, nil
			}
			if !kerrors.IsAlreadyExists(err) {
				return false, err
			}

			if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
				return false, err
			}
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("unable to create pod: %w", err)
	}
	return pod, nil
}

func waitForCompletedPodDeletion(ctx context.Context, podClient ctrlruntimeclient.Client, namespace, name string) error {
	pod := &corev1.Pod{}
	if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); kerrors.IsNotFound(err) {
		return nil
	}
	// running pods are left to run, we just wait for them to finish
	if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed && pod.DeletionTimestamp == nil {
		return nil
	}

	// delete the pod we expect, otherwise another user has relaunched this pod
	uid := pod.UID
	err := podClient.Delete(ctx, pod, ctrlruntimeclient.Preconditions(metav1.Preconditions{UID: &uid}))
	if kerrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not delete completed pod: %w", err)
	}

	return WaitForPodDeletion(ctx, podClient, namespace, name, uid)
}

func WaitForPodDeletion(ctx context.Context, podClient ctrlruntimeclient.Client, namespace, name string, uid types.UID) error {
	return wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: 2 * time.Second, Factor: 2, Steps: 10}, func() (done bool, err error) {
		pod := &corev1.Pod{}
		err = podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod)
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return true, fmt.Errorf("could not retrieve deleting pod: %w", err)
		}
		if pod.UID != uid {
			return true, nil
		}
		logrus.Debugf("Waiting for pod %s to be deleted ...", name)
		return false, nil
	})
}

func WaitForPodCompletion(ctx context.Context, podClient kubernetes.PodClient, namespace, name string, notifier ContainerNotifier, skipLogs bool) (*corev1.Pod, error) {
	if notifier == nil {
		notifier = NopNotifier
	}
	ctxDone := ctx.Done()
	notifierDone := notifier.Done(name)
	completed := make(map[string]time.Time)
	var pod *corev1.Pod
	for {
		newPod, err := waitForPodCompletionOrTimeout(ctx, podClient, namespace, name, completed, notifier, skipLogs)
		if newPod != nil {
			pod = newPod
		}
		// continue waiting if the container notifier is not yet complete for the given pod
		select {
		case <-notifierDone:
		case <-ctxDone:
		default:
			skipLogs = true
			if err == nil {
				select {
				case <-notifierDone:
				case <-ctxDone:
				case <-time.After(5 * time.Second):
				}
			}
			continue
		}
		if err != nil {
			return pod, err
		}
		break
	}
	return pod, nil
}

func waitForPodCompletionOrTimeout(ctx context.Context, podClient kubernetes.PodClient, namespace, name string, completed map[string]time.Time, notifier ContainerNotifier, skipLogs bool) (*corev1.Pod, error) {
	// Warning: this is extremely fragile, inherited legacy code.  Please be
	// careful and test thoroughly when making changes, as they very frequently
	// lead to systemic production failures.  Some guidance:
	// - There is a complex interaction between this code and the container
	//   notifier.  Updates to the state of the pod are received via the watch
	//   and communicated to the notifier.  Even in case of interruption (i.e.
	//   cancellation of `ctx`) and/or failure, events should continue to be
	//   processed until the notifier signals that it is done.  This ensures
	//   the state of the pod is correctly reported, artifacts are gathered,
	//   and termination happens deterministically for both success and failure
	//   scenarios.
	// - Since ea8f62fcf, most of the above only applies to template tests.
	//   Container and multi-stage tests now solely rely on `test-infra`'s
	//   `pod-utils` for artifact gathering and so use a notifier which
	//   instantly reports itself as done when the watched containers finish.
	pod := &corev1.Pod{}
	if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
		if kerrors.IsNotFound(err) {
			notifier.Complete(name)
			logrus.Infof("error: could not wait for pod '%s': it is no longer present on the cluster"+
				" (usually a result of a race or resource pressure. re-running the job should help)", name)
			return nil, fmt.Errorf("pod was deleted while ci-operator step was waiting for it")
		}
		return nil, fmt.Errorf("could not list pod: %w", err)
	}

	if pod.Spec.RestartPolicy == corev1.RestartPolicyAlways {
		return pod, nil
	}
	podLogNewFailedContainers(podClient, pod, completed, notifier, skipLogs)
	if podJobIsOK(pod) {
		if !skipLogs {
			logrus.Debugf("Pod %s already succeeded in %s", pod.Name, podDuration(pod).Truncate(time.Second))
		}
		return pod, nil
	}
	if podJobIsFailed(pod) {
		return pod, AppendLogToError(fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s): %s", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", "), podReason(pod)), podMessages(pod))
	}
	done := ctx.Done()

	podCheckTicker := time.NewTicker(10 * time.Second)
	defer podCheckTicker.Stop()
	var podSeenRunning bool

	for {
		select {
		case <-done:
			return pod, ctx.Err()
		case <-podCheckTicker.C:
			if err := podClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, pod); err != nil {
				if kerrors.IsNotFound(err) {
					return pod, AppendLogToError(fmt.Errorf("the pod %s/%s was deleted without completing after %s (failed containers: %s)", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", ")), podMessages(pod))
				}
				logrus.WithError(err).Warnf("Failed to get pod %s.", name)
				continue
			}

			if !podSeenRunning {
				if podHasStarted(pod) {
					podSeenRunning = true
				} else if time.Since(pod.CreationTimestamp.Time) > api.PodStartTimeout {
					message := fmt.Sprintf("pod didn't start running within %s: %s\n%s", api.PodStartTimeout, getReasonsForUnreadyContainers(pod), getEventsForPod(ctx, pod, podClient))
					logrus.Infof(message)
					notifier.Complete(name)
					return pod, results.ForReason(api.ReasonPending).ForError(errors.New(message))
				}
			}
			podLogNewFailedContainers(podClient, pod, completed, notifier, skipLogs)
			if podJobIsOK(pod) {
				if !skipLogs {
					logrus.Debugf("Pod %s succeeded after %s", pod.Name, podDuration(pod).Truncate(time.Second))
				}
				return pod, nil
			}
			if podJobIsFailed(pod) {
				return pod, AppendLogToError(fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s): %s", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", "), podReason(pod)), podMessages(pod))
			}
		}
	}
}

// podReason returns the pod's reason and message for exit or tries to find one from the pod.
func podReason(pod *corev1.Pod) string {
	reason := pod.Status.Reason
	message := pod.Status.Message
	if len(reason) == 0 {
		reason = "ContainerFailed"
	}
	if len(message) == 0 {
		message = "one or more containers exited"
	}
	return fmt.Sprintf("%s %s", reason, message)
}

// podMessages returns a string containing the messages and reasons for all terminated containers with a non-zero exit code.
func podMessages(pod *corev1.Pod) string {
	var messages []string
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if state := status.State.Terminated; state != nil && state.ExitCode != 0 {
			messages = append(messages, fmt.Sprintf("Container %s exited with code %d, reason %s", status.Name, state.ExitCode, state.Reason))
			if msg := strings.TrimSpace(state.Message); len(msg) > 0 {
				messages = append(messages, "---", msg, "---")
			}
		}
	}
	return strings.Join(messages, "\n")
}

func podDuration(pod *corev1.Pod) time.Duration {
	start := pod.Status.StartTime
	if start == nil {
		start = &pod.CreationTimestamp
	}
	var end metav1.Time
	for _, status := range pod.Status.ContainerStatuses {
		if s := status.State.Terminated; s != nil {
			if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
				end = s.FinishedAt
			}
		}
	}
	if end.IsZero() {
		for _, status := range pod.Status.InitContainerStatuses {
			if s := status.State.Terminated; s != nil && s.ExitCode != 0 {
				if end.IsZero() || s.FinishedAt.Time.After(end.Time) {
					end = s.FinishedAt
					break
				}
			}
		}
	}
	if end.IsZero() {
		end = metav1.Now()
	}
	duration := end.Sub(start.Time)
	return duration
}

func podJobIsOK(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded {
		return true
	}
	if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodUnknown {
		return false
	}
	// if all containers except artifacts are in terminated and have exit code 0, we're ok
	hasArtifacts := false
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		// don't succeed until everything has started at least once
		if status.State.Waiting != nil && status.LastTerminationState.Terminated == nil {
			return false
		}
		if status.Name == "artifacts" {
			hasArtifacts = true
			continue
		}
		s := status.State.Terminated
		if s == nil {
			return false
		}
		if s.ExitCode != 0 {
			return false
		}
	}
	if pod.Status.Phase == corev1.PodFailed && !hasArtifacts {
		return false
	}
	return true
}

func podJobIsFailed(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodFailed {
		return true
	}
	if pod.Status.Phase == corev1.PodPending || pod.Status.Phase == corev1.PodUnknown {
		return false
	}
	// if any container is in a non-zero status we have failed
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		// don't fail until everything has started at least once
		if status.State.Waiting != nil && status.LastTerminationState.Terminated == nil {
			return false
		}
		if status.Name == "artifacts" {
			continue
		}
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				return true
			}
		}
	}
	return false
}

// podHasStarted checks if a test pod can be considered as "running".
// Init containers are also checked because they can be declared in template
// tests, but those added by the test infrastructure are ignored.
func podHasStarted(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodRunning {
		return true
	}
	// Status is still `Pending` while init containers are executed.
	for _, s := range pod.Status.InitContainerStatuses {
		if s.Name != "cp-secret-wrapper" && s.State.Running != nil {
			return true
		}
	}
	return false
}

func failedContainerNames(pod *corev1.Pod) []string {
	var names []string
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if s := status.State.Terminated; s != nil {
			if s.ExitCode != 0 {
				names = append(names, status.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func podLogNewFailedContainers(podClient kubernetes.PodClient, pod *corev1.Pod, completed map[string]time.Time, notifier ContainerNotifier, skipLogs bool) {
	var statuses []corev1.ContainerStatus
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)

	for _, status := range statuses {
		if _, ok := completed[status.Name]; ok {
			continue
		}
		s := status.State.Terminated
		if s == nil {
			continue
		}
		completed[status.Name] = s.FinishedAt.Time
		notifier.Notify(pod, status.Name)

		if s.ExitCode == 0 {
			if !skipLogs {
				logrus.Debugf("Container %s in pod %s completed successfully", status.Name, pod.Name)
			}
			continue
		}

		if s, err := podClient.GetLogs(pod.Namespace, pod.Name, &corev1.PodLogOptions{
			Container: status.Name,
		}).Stream(context.TODO()); err == nil {
			logs := &bytes.Buffer{}
			if _, err := io.Copy(logs, s); err != nil {
				logrus.WithError(err).Warnf("Unable to copy log output from failed pod container %s.", status.Name)
			}
			if err := s.Close(); err != nil {
				logrus.WithError(err).Warnf("Unable to close log output from failed pod container %s.", status.Name)
			}
			logrus.Infof("Logs for container %s in pod %s:", status.Name, pod.Name)
			logrus.Info(logs.String())
		} else {
			logrus.WithError(err).Warnf("error: Unable to retrieve logs from failed pod container %s.", status.Name)
		}

		logrus.Debugf("Container %s in pod %s failed, exit code %d, reason %s", status.Name, pod.Name, status.State.Terminated.ExitCode, status.State.Terminated.Reason)
	}
	// Workaround for https://github.com/kubernetes/kubernetes/issues/88611
	// Pods may be terminated with DeadlineExceeded with spec.ActiveDeadlineSeconds is set.
	// However this doesn't change container statuses, so len(podRunningContainers(pod) is never 0.
	// Notify the test is complete if ActiveDeadlineSeconds is set and pod has failed.
	if pod.Status.Phase == corev1.PodFailed && pod.Spec.ActiveDeadlineSeconds != nil {
		notifier.Complete(pod.Name)
	}
	// if there are no running containers and we're in a terminal state, mark the pod complete
	if (pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded) && len(podRunningContainers(pod)) == 0 {
		notifier.Complete(pod.Name)
	}
}

func podRunningContainers(pod *corev1.Pod) []string {
	var names []string
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if status.State.Running != nil || status.State.Waiting != nil || status.State.Terminated == nil {
			continue
		}
		names = append(names, status.Name)
	}
	return names
}

func getReasonsForUnreadyContainers(p *corev1.Pod) string {
	builder := &strings.Builder{}
	for _, c := range p.Status.ContainerStatuses {
		if c.Ready {
			continue
		}
		var reason, message string
		switch {
		case c.State.Waiting != nil:
			reason = c.State.Waiting.Reason
			message = c.State.Waiting.Message
		case c.State.Running != nil:
			reason = c.State.Waiting.Reason
			message = c.State.Waiting.Message
		case c.State.Terminated != nil:
			reason = c.State.Terminated.Reason
			message = c.State.Terminated.Message
		default:
			reason = "unknown"
			message = "unknown"
		}
		if message != "" {
			message = fmt.Sprintf(" and message %s", message)
		}
		_, _ = builder.WriteString(fmt.Sprintf("\n* Container %s is not ready with reason %s%s", c.Name, reason, message))
	}
	return builder.String()
}

func getEventsForPod(ctx context.Context, pod *corev1.Pod, client ctrlruntimeclient.Client) string {
	events := &corev1.EventList{}
	listOpts := &ctrlruntimeclient.ListOptions{
		Namespace:     pod.Namespace,
		FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", string(pod.GetUID())),
	}
	if err := client.List(ctx, events, listOpts); err != nil {
		logrus.WithError(err).Warn("Could not fetch events.")
		return ""
	}
	builder := &strings.Builder{}
	_, _ = builder.WriteString(fmt.Sprintf("Found %d events for Pod %s:", len(events.Items), pod.Name))
	for _, event := range events.Items {
		_, _ = builder.WriteString(fmt.Sprintf("\n* %dx %s: %s", event.Count, event.Source.Component, event.Message))
	}
	return builder.String()
}
