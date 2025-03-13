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
	if err := s.runPods(ctx, pods, bestEffortSteps); err != nil {
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

func (s *multiStageTestStep) runPods(ctx context.Context, pods []coreapi.Pod, bestEffortSteps sets.Set[string]) error {
	var errs []error
	for _, pod := range pods {
		err := s.runPod(ctx, &pod, base_steps.NewTestCaseNotifier(util.NopNotifier), util.WaitForPodFlag(0))
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
			err := s.runPod(textCtx, &p, base_steps.NewTestCaseNotifier(util.NopNotifier), util.Interruptible)
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

func (s *multiStageTestStep) runPod(ctx context.Context, pod *coreapi.Pod, notifier *base_steps.TestCaseNotifier, flags util.WaitForPodFlag) error {
	start := time.Now()
	logrus.Infof("Running step %s.", pod.Name)
	client := s.client.WithNewLoggingClient()
	if _, err := util.CreateOrRestartPod(ctx, client, pod); err != nil {
		return fmt.Errorf("failed to create or restart %s pod: %w", pod.Name, err)
	}
	newPod, err := util.WaitForPodCompletion(ctx, client, pod.Namespace, pod.Name, notifier, flags)
	if newPod != nil {
		pod = newPod
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
	s.subTests = append(s.subTests, notifier.SubTests(fmt.Sprintf("%s - %s ", s.Description(), pod.Name))...)
	s.subLock.Unlock()
	if err != nil {
		linksText := strings.Builder{}
		linksText.WriteString(fmt.Sprintf("Link to step on registry info site: https://steps.ci.openshift.org/reference/%s", strings.TrimPrefix(pod.Name, s.name+"-")))
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
