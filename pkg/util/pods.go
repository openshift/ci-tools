package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
)

// WaitForPodFlag changes the behavior of the functions which monitor pods
type WaitForPodFlag uint8

const (
	// SkipLogs omits informational logs, such as when the pod is part of a
	// larger step like release creation where displaying pod specific info is
	// confusing to an end user. Failure logs are still printed.
	SkipLogs WaitForPodFlag = 1 << iota
	// Interruptible indicates this pod is expected to potentially be cancelled
	// Used for observer pods so that their cancellation is not reported as
	// abnormal.
	Interruptible
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
	return wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: 2 * time.Second, Factor: 2, Steps: 10}, func(ctx context.Context) (done bool, err error) {
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

func WaitForPodCompletion(ctx context.Context, podClient kubernetes.PodClient, namespace, name string, notifier ContainerNotifier, flags WaitForPodFlag) (*corev1.Pod, error) {
	if notifier == nil {
		notifier = NopNotifier
	}

	metricsAgent := podClient.MetricsAgent()
	metricsAgent.AddNodeWorkload(ctx, namespace, name, name, podClient)
	defer metricsAgent.RemoveNodeWorkload(name)

	ctxDone := ctx.Done()
	notifierDone := notifier.Done(name)
	completed := make(map[string]time.Time)
	var pod *corev1.Pod
	for {
		newPod, err := waitForPodCompletionOrTimeout(ctx, podClient, namespace, name, completed, notifier, flags)
		if newPod != nil {
			pod = newPod
		}
		// continue waiting if the container notifier is not yet complete for the given pod
		select {
		case <-notifierDone:
		case <-ctxDone:
		default:
			flags |= SkipLogs
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
			podClient.MetricsAgent().StorePodLifecycleMetrics(pod.Name, pod.Namespace)
			return pod, err
		}
		break
	}

	podClient.MetricsAgent().StorePodLifecycleMetrics(pod.Name, pod.Namespace)
	return pod, nil
}

func waitForPodCompletionOrTimeout(ctx context.Context, podClient kubernetes.PodClient, namespace, name string, completed map[string]time.Time, notifier ContainerNotifier, flags WaitForPodFlag) (*corev1.Pod, error) {
	var ret atomic.Pointer[corev1.Pod]
	var eg *errgroup.Group
	eg, ctx = errgroup.WithContext(ctx)
	pendingCtx, cancel := context.WithCancel(ctx)
	pendingCheck := func() error {
		timeout := podClient.GetPendingTimeout()
		if pod, err := checkPendingPeriodic(pendingCtx.Done(), timeout, &ret); err != nil {
			err = fmt.Errorf("pod pending for more than %s: %w: %s\n%s", timeout, err, getReasonsForUnreadyContainers(pod), getEventsForPod(ctx, pod, podClient))
			logrus.Info(err)
			notifier.Complete(pod.Name)
			return err
		}
		return nil
	}
	eg.Go(func() error {
		defer cancel()
		if err := kubernetes.WaitForConditionOnObject(ctx, podClient, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, &corev1.PodList{}, &corev1.Pod{}, func(obj runtime.Object) (bool, error) {
			pod := obj.(*corev1.Pod)
			// Start the periodic pending checks as soon as a pod object is
			// available.  This will happen (once) after the initial list.
			if ret.Swap(pod) == nil {
				eg.Go(pendingCheck)
			}
			return processPodEvent(ctx, podClient, completed, notifier, flags, pod)
		}, 0); err != nil {
			if errors.Is(err, wait.ErrWaitTimeout) {
				err = ctx.Err()
			} else if kerrors.IsNotFound(err) {
				notifier.Complete(name)
				logrus.Infof("error: could not wait for pod '%s': it is no longer present on the cluster"+
					" (usually a result of a race or resource pressure. re-running the job should help)", name)
			}
			return fmt.Errorf("could not watch pod: %w", err)
		}
		return nil
	})
	err := eg.Wait()
	return ret.Load(), err
}

func processPodEvent(
	ctx context.Context,
	podClient kubernetes.PodClient,
	completed map[string]time.Time,
	notifier ContainerNotifier,
	flags WaitForPodFlag,
	pod *corev1.Pod,
) (done bool, err error) {
	if pod.Spec.RestartPolicy == corev1.RestartPolicyAlways {
		return true, nil
	}
	podLogNewFailedContainers(podClient, pod, completed, notifier)
	podLogDeletion(ctx, podClient, flags, *pod)
	if podJobIsOK(pod) {
		logrus.Debugf("Pod %s succeeded after %s", pod.Name, podDuration(pod).Truncate(time.Second))
		return true, nil
	}
	if podJobIsFailed(pod) {
		return true, AppendLogToError(fmt.Errorf("the pod %s/%s failed after %s (failed containers: %s): %s", pod.Namespace, pod.Name, podDuration(pod).Truncate(time.Second), strings.Join(failedContainerNames(pod), ", "), podReason(pod)), podMessages(pod))
	}
	return false, nil
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

// checkPendingPeriodic continually calls checkPending
// After each verification is performed based on the value loaded from the
// pointer, the timer is reset based on the result or an error is returned.
// This function only returns when `done` is signaled or an error occurs; for
// the latter case, the pod which caused the failure is also returned.
func checkPendingPeriodic(
	done <-chan struct{},
	timeout time.Duration,
	pod *atomic.Pointer[corev1.Pod],
) (*corev1.Pod, error) {
	timer := time.NewTimer(0)
	for {
		select {
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
			return nil, nil
		case <-timer.C:
			// This is based on the invariant that the time point at which the
			// verification should be performed only moves forward with changes
			// in pod status.  Whenever the timer expires, the latest version
			// of the pod is examined and one of two cases can occur:
			// - No containers have have started/finished during the waiting
			//   period.  Since the period was the pending timeout, this means
			//   `checkPending` will return an error.
			// - More likely, one or more containers started/finished during
			//   the waiting period.  This means the point at which the test
			//   can fail due to a pending timeout will move forward in time,
			//   based on the current state of the pod (i.e. the return value
			//   of `checkPending`).  The timer can then be reset based on that
			//   time.
			pod, now := pod.Load(), time.Now()
			if next, err := checkPending(*pod, timeout, now); err != nil {
				return pod, err
			} else {
				timer.Reset(next.Sub(now))
			}
		}
	}
}

// checkPending checks if a pod has been pending for too long
// The purpose of this function is to cause a failure when a pod is found to be
// potentially permanently "pending", so that the test fails earlier than its
// timeout period, which is usually much longer to accommodate legitimate tests.
//
// A pod can be in one of the following states (as described in
// https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#pod-phase):
//
//   - Running/succeeded/failed: all containers have successfully started, no
//     error is returned.
//   - Unknown: pod state cannot be recovered, likely indicating a cluster
//     failure.  This state has been deprecated since Kubernetes 1.22
//     (https://relnotes.k8s.io/?kinds=deprecation&releaseVersions=1.22.0).
//     This implementation chooses to ignore it.
//   - Pending: either "init" containers are being executed or they are done and
//     not all containers have started.  The verification described below is
//     performed.
//
// Individual containers in a pending pod can be in either the waiting,
// running, or terminated state.  Only those in the first state are considered
// by this function.  The verification performed is different for "init" and
// regular containers:
//
//   - "Init" containers execute serially, so they are checked in sequence.  If
//     any is found to be "waiting" for more than the maximum period, relative
//     to the finishing time of the previous (or to the creation time of the
//     pod, when checking the first), an error is returned.
//   - Regular containers are started in parallel.  If any is found to be
//     "waiting" for more than the maximum period, relative to the finishing
//     time of the last "init" container (or to the creation time of the pod,
//     if there are none), an error is returned.
//
// If the pod is considered to be acceptable, the time at which the next check
// should be performed (i.e. after which this function may return an error) is
// returned.  This can be used to schedule the next call.
func checkPending(pod corev1.Pod, timeout time.Duration, now time.Time) (time.Time, error) {
	switch pod.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return time.Time{}, nil
	case corev1.PodUnknown:
		logrus.Warningf(`received status "unknown" for pod %s`, pod.Name)
		fallthrough
	case corev1.PodRunning:
		return now.Add(timeout), nil
	case corev1.PodPending:
	default:
		panic(fmt.Sprintf("unknown pod phase: %s", pod.Status.Phase))
	}
	check := func(t0 time.Time) (time.Time, error) {
		if t := t0.Add(timeout); now.Before(t) {
			return t, nil
		}
		names := strings.Join(pendingContainerNames(pod), ", ")
		return time.Time{}, results.ForReason(api.ReasonPending).ForError(fmt.Errorf("containers have not started in %s: %s", now.Sub(t0), names))
	}
	prev := pod.CreationTimestamp.Time
	for _, s := range pod.Status.InitContainerStatuses {
		if s.State.Running != nil {
			return now.Add(timeout), nil
		} else if w := s.State.Waiting; w != nil {
			return check(prev)
		} else if t := s.State.Terminated; t != nil {
			prev = t.FinishedAt.Time
		} else {
			panic(fmt.Sprintf("invalid container status: %#v", s))
		}
	}
	for _, s := range pod.Status.ContainerStatuses {
		if s.State.Waiting != nil {
			if ret, err := check(prev); err != nil {
				return ret, err
			}
		}
	}
	return prev.Add(timeout), nil
}

func pendingContainerNames(pod corev1.Pod) []string {
	return containerNamesInState(pod, func(s corev1.ContainerStatus) bool {
		return s.State.Waiting != nil
	})
}

func failedContainerNames(pod *corev1.Pod) []string {
	return containerNamesInState(*pod, func(s corev1.ContainerStatus) bool {
		t := s.State.Terminated
		return t != nil && t.ExitCode != 0
	})
}

func containerNamesInState(pod corev1.Pod, p func(corev1.ContainerStatus) bool) []string {
	var names []string
	for _, status := range append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if p(status) {
			names = append(names, status.Name)
		}
	}
	sort.Strings(names)
	return names
}

func podLogNewFailedContainers(podClient kubernetes.PodClient, pod *corev1.Pod, completed map[string]time.Time, notifier ContainerNotifier) {
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
			logrus.Debugf("Container %s in pod %s completed successfully", status.Name, pod.Name)
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

func podLogDeletion(
	ctx context.Context,
	podClient kubernetes.PodClient,
	flags WaitForPodFlag,
	pod corev1.Pod,
) {
	if pod.DeletionTimestamp == nil {
		return
	}
	if IsBitSet(flags, Interruptible) {
		logrus.Debugf("Pod %s is being deleted as expected", pod.Name)
	} else {
		var f func(string, ...interface{})
		if IsBitSet(flags, SkipLogs) {
			f = logrus.Debugf
		} else {
			f = logrus.Warningf
		}
		f("Pod %s is being unexpectedly deleted:\n%s", pod.Name, getEventsForPod(ctx, &pod, podClient))
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
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Found %d events for Pod %s:", len(events.Items), pod.Name))
	for _, event := range events.Items {
		builder.WriteString(fmt.Sprintf("\n* %s %dx %s: %s", event.LastTimestamp.Format(time.RFC3339), event.Count, event.Source.Component, event.Message))
	}
	return builder.String()
}
