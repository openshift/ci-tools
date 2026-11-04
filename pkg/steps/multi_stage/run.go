package multi_stage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	base_steps "github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

func (s *multiStageTestStep) runSteps(
	ctx context.Context,
	phase string,
	steps []api.LiteralTestStep,
	env []coreapi.EnvVar,
	secretVolumes []coreapi.Volume,
	secretVolumeMounts []coreapi.VolumeMount,
) error {
	start := time.Now()
	logrus.Infof("Running multi-stage phase %s", phase)
	pods, bestEffortSteps, err := s.generatePods(steps, env, secretVolumes, secretVolumeMounts, &generatePodOptions{
		enableSecretsStoreCSIDriver: s.enableSecretsStoreCSIDriver,
	})
	if err != nil {
		s.flags |= hasPrevErrs
		return err
	}
	var errs []error
	defer func() {
		if len(errs) != 0 {
			s.flags |= hasPrevErrs
		}
	}()
	if err := s.runPods(ctx, phase, pods, bestEffortSteps); err != nil {
		errs = append(errs, err)
	}
	select {
	case <-ctx.Done():
		logrus.Infof("cleanup: Deleting pods with label %s=%s", MultiStageTestLabel, s.name)
		if err := s.client.DeleteAllOf(base_steps.CleanupCtx, &coreapi.Pod{}, ctrlruntimeclient.InNamespace(s.jobSpec.Namespace()), ctrlruntimeclient.MatchingLabels{MultiStageTestLabel: s.name}); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete pods with label %s=%s: %w", MultiStageTestLabel, s.name, err))
		}
		errs = append(errs, fmt.Errorf("cancelled"))
	default:
		break
	}

	err = utilerrors.NewAggregate(errs)
	finished := time.Now()
	duration := finished.Sub(start)
	testCase := &junit.TestCase{
		Name:      fmt.Sprintf("Run multi-stage test %s phase", phase),
		Duration:  duration.Seconds(),
		SystemOut: fmt.Sprintf("The collected steps of multi-stage phase %s.", phase),
	}
	verb := "succeeded"
	if err != nil {
		verb = "failed"
		testCase.FailureOutput = &junit.FailureOutput{
			Output: err.Error(),
		}
	}
	s.subTests = append(s.subTests, testCase)
	logrus.Infof("Step phase %s %s after %s.", phase, verb, duration.Truncate(time.Second))

	return err
}

func (s *multiStageTestStep) runPods(ctx context.Context, phase string, pods []coreapi.Pod, bestEffortSteps sets.Set[string]) error {
	var errs []error
	for _, pod := range pods {
		err := s.runPod(ctx, &pod, base_steps.NewTestCaseNotifier(util.NopNotifier), util.WaitForPodFlag(0), phase == "post")
		if err == nil {
			continue
		}
		if bestEffortSteps != nil && bestEffortSteps.Has(pod.Name) {
			logrus.Infof("Pod %s is running in best-effort mode, ignoring the failure...", pod.Name)
			continue
		}
		errs = append(errs, err)
		if s.flags&shortCircuit != 0 {
			break
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) runObservers(ctx, textCtx context.Context, pods []coreapi.Pod, done chan<- struct{}) {
	wg := sync.WaitGroup{}
	wg.Add(len(pods))
	errs := make(chan error, len(pods))
	for _, pod := range pods {
		go func(p coreapi.Pod) {
			<-ctx.Done()
			logrus.Infof("Signalling observer pod %q to terminate...", p.Name)
			if err := s.client.Delete(context.Background(), &p); err != nil {
				logrus.WithError(err).Warn("failed to trigger observer to stop")
			}
		}(pod)
		go func(p coreapi.Pod) {
			err := s.runPod(textCtx, &p, base_steps.NewTestCaseNotifier(util.NopNotifier), util.Interruptible, false)
			if ctx.Err() == nil {
				// when the observer is cancelled, we get an error here that we need to ignore, as it's not an error
				// for the Pod to be deleted when it's cancelled, it's just expected
				errs <- err
			} else {
				logrus.Debugf("ignoring observer error after cancellation: %v", err)
			}
			wg.Done()
		}(pod)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			logrus.WithError(err).Warn("observer failed")
		}
	}
	done <- struct{}{}
}

func (s *multiStageTestStep) runPod(ctx context.Context, pod *coreapi.Pod, notifier *base_steps.TestCaseNotifier, flags util.WaitForPodFlag, retryStartup bool) error {
	junitName, err := s.junitNameForPod(pod)
	if err != nil {
		return err
	}

	start := time.Now()
	template := pod.DeepCopy()
	logrus.Infof("Running step %s.", pod.Name)
	client := s.client.WithNewLoggingClient()
	attemptClient := &startupAttemptClient{PodClient: client}

	client.MetricsAgent().StoreMachinesSnapshot(pod)

	waitClient := client
	if retryStartup {
		waitClient = attemptClient
	}
	attemptClient.creating = true
	_, err = util.CreateOrRestartPod(ctx, waitClient, pod)
	attemptClient.creating = false
	if err != nil {
		err = fmt.Errorf("failed to create or restart %s pod: %w", pod.Name, err)
	} else {
		var newPod *coreapi.Pod
		newPod, err = util.WaitForPodCompletion(ctx, waitClient, pod.Namespace, pod.Name, notifier, flags)
		if newPod != nil {
			pod = newPod
		}
	}
	var replacement *coreapi.Pod
	if retryStartup && attemptClient.watchEstablished.Load() && !attemptClient.apiFailed.Load() && !attemptClient.executionObserved.Load() && !attemptClient.existingPodObserved.Load() && err != nil && ctx.Err() == nil {
		inspectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var inspectErr error
		replacement, inspectErr = startupRetryPod(inspectCtx, client, template, pod, err)
		cancel()
		if inspectErr != nil {
			err = utilerrors.NewAggregate([]error{err, inspectErr})
		}
	}

	// If we got an error and the Pod is still pending (failed to schedule or failed to start all containers),
	// delete it to prevent it from recovering and potentially executing later, potentially simultaneously
	// with pods we run later (for example, we do not want to leave a Pending unschedulable cluster-testing
	// step Pod in the cluster, because it may eventually get scheduled and start testing the cluster that
	// is already being torn down).
	if err != nil && pod.Status.Phase == coreapi.PodPending {
		client.MetricsAgent().StorePodLifecycleMetrics(pod.Name, pod.Namespace, coreapi.PodFailed)
		client.MetricsAgent().StoreMachinesSnapshot(pod)
		logrus.Infof("Deleting pod %s that failed to start", pod.Name)
		deleteCtx, cancel := context.WithTimeout(base_steps.CleanupCtx, 30*time.Second)
		defer cancel()
		deleteOptions := []ctrlruntimeclient.DeleteOption{}
		if replacement != nil {
			deleteOptions = append(deleteOptions, ctrlruntimeclient.Preconditions{UID: &pod.UID, ResourceVersion: &pod.ResourceVersion})
		}
		if delErr := client.Delete(deleteCtx, pod, deleteOptions...); delErr != nil {
			if !kerrors.IsNotFound(delErr) {
				logrus.WithError(delErr).Warnf("Failed to delete pending pod %s", pod.Name)
			}
			if replacement != nil {
				err = utilerrors.NewAggregate([]error{err, fmt.Errorf("deleting startup retry pod: %w", delErr)})
				replacement = nil
				if kerrors.IsConflict(delErr) {
					if cleanupErr := cleanupConflictedPod(deleteCtx, client, pod); cleanupErr != nil {
						err = utilerrors.NewAggregate([]error{err, fmt.Errorf("cleaning up conflicted pod: %w", cleanupErr)})
					}
				}
			}
		} else if replacement != nil {
			waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
			waitErr := util.WaitForPodDeletion(waitCtx, startupDeletionClient{Client: client, uid: pod.UID}, pod.Namespace, pod.Name, pod.UID)
			waitCancel()
			if waitErr != nil {
				err = utilerrors.NewAggregate([]error{err, fmt.Errorf("waiting for startup retry pod deletion: %w", waitErr)})
				replacement = nil
			}
		}
	}

	finished := time.Now()
	duration := finished.Sub(start)
	verb := "succeeded"
	if err != nil {
		verb = "failed"
	}
	logrus.Infof("Step %s %s after %s.", pod.Name, verb, duration.Truncate(time.Second))
	s.subLock.Lock()
	s.subSteps = append(s.subSteps, api.CIOperatorStepDetailInfo{
		StepName:    pod.Name,
		Description: fmt.Sprintf("Run pod %s", pod.Name),
		StartedAt:   &start,
		FinishedAt:  &finished,
		Duration:    &duration,
		Failed:      utilpointer.Bool(err != nil),
		Manifests:   client.Objects(),
	})
	attemptTests := notifier.SubTestsWithName(junitName)
	if len(attemptTests) == 0 && err != nil {
		attemptName := junitName
		if replacement != nil {
			attemptName += " startup attempt 1"
		}
		attemptTests = append(attemptTests, &junit.TestCase{
			Name:          attemptName,
			Duration:      duration.Seconds(),
			FailureOutput: &junit.FailureOutput{Output: err.Error()},
		})
	}
	s.subTests = append(s.subTests, attemptTests...)
	s.subLock.Unlock()
	if replacement != nil && ctx.Err() == nil {
		logrus.WithField("pod", pod.Name).WithField("replacement", replacement.Name).Warn("Retrying never-started post pod on another node")
		if retryErr := s.runPod(ctx, replacement, base_steps.NewTestCaseNotifier(util.NopNotifier), flags, false); retryErr != nil {
			return utilerrors.NewAggregate([]error{err, retryErr})
		}
		return nil
	}
	if err != nil {
		linksText := strings.Builder{}
		linksText.WriteString(fmt.Sprintf("Link to step on registry info site: https://steps.ci.openshift.org/reference/%s", pod.Labels[base_steps.LabelMetadataStep]))
		linksText.WriteString(fmt.Sprintf("\nLink to job on registry info site: https://steps.ci.openshift.org/job?org=%s&repo=%s&branch=%s&test=%s", s.config.Metadata.Org, s.config.Metadata.Repo, s.config.Metadata.Branch, s.name))
		if s.config.Metadata.Variant != "" {
			linksText.WriteString(fmt.Sprintf("&variant=%s", s.config.Metadata.Variant))
		}
		status := "failed"
		if pod.Status.Phase == coreapi.PodFailed && pod.Status.Reason == "DeadlineExceeded" {
			status = "exceeded the configured timeout"
			if pod.Spec.ActiveDeadlineSeconds != nil {
				status = fmt.Sprintf("%s activeDeadlineSeconds=%d", status, *pod.Spec.ActiveDeadlineSeconds)
			}
		}
		return fmt.Errorf("%q pod %q %s: %w\n%s", s.name, pod.Name, status, err, linksText.String())
	}
	return nil
}

func (s *multiStageTestStep) junitNameForPod(pod *coreapi.Pod) (string, error) {
	if pod == nil {
		return "", fmt.Errorf("multi-stage test %q cannot determine JUnit name for nil pod", s.name)
	}
	stepName := pod.Labels[base_steps.LabelMetadataStep]
	if stepName == "" {
		return "", fmt.Errorf("multi-stage test %q pod %q is missing required label %q", s.name, pod.Name, base_steps.LabelMetadataStep)
	}
	return fmt.Sprintf("Run multi-stage step %s", stepName), nil
}
