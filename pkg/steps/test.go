package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

type testStep struct {
	config      api.TestStepConfiguration
	resources   api.ResourceConfiguration
	podClient   PodClient
	istClient   imageclientset.ImageStreamTagsGetter
	artifactDir string
	jobSpec     *JobSpec
}

func (s *testStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *testStep) Run(ctx context.Context, dry bool) error {
	log.Printf("Executing test %s", s.config.As)

	containerResources, err := resourcesFor(s.resources.RequirementsForStep(s.config.As))
	if err != nil {
		return fmt.Errorf("unable to calculate test pod resources for %s: %s", s.config.As, err)
	}

	pod := &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name: s.config.As,
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:      "test",
					Image:     fmt.Sprintf("%s:%s", PipelineImageStream, s.config.From),
					Command:   []string{"/bin/sh", "-c", "#!/bin/sh\nset -eu\n" + s.config.Commands},
					Resources: containerResources,
				},
			},
		},
	}

	// when the test container terminates and artifact directory has been set, grab everything under the directory
	var notifier ContainerNotifier = NopNotifier
	if s.gatherArtifacts() {
		artifacts := NewArtifactWorker(s.podClient, filepath.Join(s.artifactDir, s.config.As), s.jobSpec.Namespace())
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      "artifacts",
			MountPath: s.config.ArtifactDir,
		})
		addArtifactsContainer(pod, s.config.ArtifactDir)
		artifacts.CollectFromPod(pod.Name, []string{"test"}, nil)
		notifier = artifacts
	}

	if owner := s.jobSpec.Owner(); owner != nil {
		pod.OwnerReferences = append(pod.OwnerReferences, *owner)
	}

	if dry {
		j, _ := json.MarshalIndent(pod, "", "  ")
		log.Printf("pod:\n%s", j)
		return nil
	}

	go func() {
		<-ctx.Done()
		notifier.Cancel()
		log.Printf("cleanup: Deleting test pod %s", s.config.As)
		if err := s.podClient.Pods(s.jobSpec.Namespace()).Delete(s.config.As, nil); err != nil && !errors.IsNotFound(err) {
			log.Printf("error: Could not delete test pod: %v", err)
		}
	}()

	pod, err = createOrRestartPod(s.podClient.Pods(s.jobSpec.Namespace()), pod)
	if err != nil {
		return err
	}

	if err := waitForPodCompletion(s.podClient.Pods(s.jobSpec.Namespace()), pod.Name, notifier); err != nil {
		return err
	}

	return nil
}

func (s *testStep) gatherArtifacts() bool {
	return len(s.config.ArtifactDir) > 0 && len(s.artifactDir) > 0
}

func (s *testStep) Done() (bool, error) {
	ready, err := isPodCompleted(s.podClient.Pods(s.jobSpec.Namespace()), s.config.As)
	if err != nil {
		return false, err
	}
	if !ready {
		return false, nil
	}
	return true, nil
}

func (s *testStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *testStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *testStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *testStep) Name() string { return s.config.As }

func TestStep(config api.TestStepConfiguration, resources api.ResourceConfiguration, podClient PodClient, artifactDir string, jobSpec *JobSpec) api.Step {
	return &testStep{
		config:      config,
		resources:   resources,
		podClient:   podClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}
