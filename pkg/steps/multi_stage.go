package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
)

const (
	multiStageTestLabel = "ci.openshift.io/multi-stage-test"
)

type multiStageTestStep struct {
	dry             bool
	name            string
	config          *api.ReleaseBuildConfiguration
	podClient       PodClient
	jobSpec         *api.JobSpec
	pre, test, post []api.TestStep
}

func MultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	podClient PodClient,
	jobSpec *api.JobSpec,
) api.Step {
	return newMultiStageTestStep(testConfig, config, podClient, jobSpec)
}

func newMultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	podClient PodClient,
	jobSpec *api.JobSpec,
) *multiStageTestStep {
	return &multiStageTestStep{
		name:      testConfig.As,
		config:    config,
		podClient: podClient,
		jobSpec:   jobSpec,
		pre:       testConfig.MultiStageTestConfiguration.Pre,
		test:      testConfig.MultiStageTestConfiguration.Test,
		post:      testConfig.MultiStageTestConfiguration.Post,
	}
}

func (s *multiStageTestStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *multiStageTestStep) Run(ctx context.Context, dry bool) error {
	s.dry = dry
	var errs []error
	if err := s.runSteps(ctx, s.pre, true); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %v", s.name, err))
	} else if err := s.runSteps(ctx, s.test, true); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %v", s.name, err))
	}
	if err := s.runSteps(ctx, s.post, false); err != nil {
		errs = append(errs, fmt.Errorf("%q post steps failed: %v", s.name, err))
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) Done() (bool, error) { return false, nil }
func (s *multiStageTestStep) Name() string        { return s.name }
func (s *multiStageTestStep) Description() string {
	return fmt.Sprintf("Run multi-stage test %s", s.name)
}

func (s *multiStageTestStep) Requires() (ret []api.StepLink) {
	var needsImages, needsRelease bool
	for _, step := range append(append(s.pre, s.test...), s.post...) {
		if s.config.IsPipelineImage(step.From) {
			ret = append(ret, api.InternalImageLink(api.PipelineImageStreamTagReference(step.From)))
		} else if s.config.BuildsImage(step.From) {
			needsImages = true
		} else {
			needsRelease = true
		}
	}
	if needsImages {
		ret = append(ret, api.ImagesReadyLink())
	}
	if needsRelease {
		ret = append(ret, api.ReleaseImagesLink())
	}
	return
}

func (s *multiStageTestStep) Creates() []api.StepLink { return nil }
func (s *multiStageTestStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}
func (s *multiStageTestStep) SubTests() []*junit.TestCase { return nil }

func (s *multiStageTestStep) runSteps(ctx context.Context, steps []api.TestStep, shortCircuit bool) error {
	pods, err := s.generatePods(steps)
	if err != nil {
		return err
	}
	return s.runPods(ctx, pods, shortCircuit)
}

func (s *multiStageTestStep) generatePods(steps []api.TestStep) ([]coreapi.Pod, error) {
	var ret []coreapi.Pod
	var errs []error
	for _, step := range steps {
		image := step.From
		if s.config.IsPipelineImage(image) {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, image)
		}
		resources, err := resourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		pod, err := generateBasePod(s.jobSpec, name, step.As, []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + step.Commands}, image, resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		pod.Labels[multiStageTestLabel] = s.name
		container := &pod.Spec.Containers[0]
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "NAMESPACE", Value: s.jobSpec.Namespace},
			{Name: "JOB_NAME_SAFE", Value: strings.Replace(s.name, "_", "-", -1)},
			{Name: "JOB_NAME_HASH", Value: s.jobSpec.JobNameHash()},
		}...)
		if owner := s.jobSpec.Owner(); owner != nil {
			pod.OwnerReferences = append(pod.OwnerReferences, *owner)
		}
		ret = append(ret, *pod)
	}
	return ret, utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) runPods(ctx context.Context, pods []coreapi.Pod, shortCircuit bool) error {
	go func() {
		<-ctx.Done()
		log.Printf("cleanup: Deleting pods with label %s=%s", multiStageTestLabel, s.name)
		if !s.dry {
			if err := deletePods(s.podClient.Pods(s.jobSpec.Namespace), s.name); err != nil {
				log.Printf("failed to delete pods with label %s=%s: %v", multiStageTestLabel, s.name, err)
			}
		}
	}()
	var errs []error
	for _, pod := range pods {
		log.Printf("Executing %q", pod.Name)
		if err := s.runPod(ctx, &pod); err != nil {
			errs = append(errs, err)
			if shortCircuit {
				break
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) runPod(ctx context.Context, pod *coreapi.Pod) error {
	if s.dry {
		return dumpObject(pod)
	}
	if _, err := createOrRestartPod(s.podClient.Pods(s.jobSpec.Namespace), pod); err != nil {
		return fmt.Errorf("failed to create or restart %q pod: %v", pod.Name, err)
	}
	if err := waitForPodCompletion(s.podClient.Pods(s.jobSpec.Namespace), pod.Name, nil, false); err != nil {
		return fmt.Errorf("%q pod %q failed: %v", s.name, pod.Name, err)
	}
	return nil
}

func deletePods(client coreclientset.PodInterface, test string) error {
	err := client.DeleteCollection(
		&meta.DeleteOptions{},
		meta.ListOptions{
			LabelSelector: fields.Set{
				multiStageTestLabel: test,
			}.AsSelector().String(),
		},
	)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

func dumpObject(obj runtime.Object) error {
	j, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to convert object to JSON: %v", err)
	}
	log.Print(string(j))
	return nil
}
