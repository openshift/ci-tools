package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-operator/pkg/api"
	"github.com/openshift/ci-operator/pkg/junit"
)

const testSecretName = "test-secret"
const testSecretDefaultPath = "/usr/test-secrets"

type PodStepConfiguration struct {
	As                 string
	From               api.ImageStreamTagReference
	Commands           string
	ArtifactDir        string
	ServiceAccountName string
	Secret             api.Secret
	MemoryBackedVolume *api.MemoryBackedVolume
}

type podStep struct {
	name        string
	config      PodStepConfiguration
	resources   api.ResourceConfiguration
	podClient   PodClient
	istClient   imageclientset.ImageStreamTagsGetter
	artifactDir string
	jobSpec     *api.JobSpec

	subTests []*junit.TestCase
}

func (s *podStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *podStep) Run(ctx context.Context, dry bool) error {
	log.Printf("Executing %s %s", s.name, s.config.As)

	containerResources, err := resourcesFor(s.resources.RequirementsForStep(s.config.As))
	if err != nil {
		return fmt.Errorf("unable to calculate %s pod resources for %s: %s", s.name, s.config.As, err)
	}

	if len(s.config.From.Namespace) > 0 {
		return fmt.Errorf("pod step does not supported an image stream tag reference outside the namespace")
	}
	image := fmt.Sprintf("%s:%s", s.config.From.Name, s.config.From.Tag)

	pod, err := s.generatePodForStep(image, containerResources)
	if err != nil {
		return fmt.Errorf("pod step was invalid: %v", err)
	}

	// when the test container terminates and artifact directory has been set, grab everything under the directory
	var notifier ContainerNotifier = NopNotifier
	if s.gatherArtifacts() {
		artifacts := NewArtifactWorker(s.podClient, filepath.Join(s.artifactDir, s.config.As), s.jobSpec.Namespace)
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      "artifacts",
			MountPath: s.config.ArtifactDir,
		})
		addArtifactsContainer(pod, s.config.ArtifactDir)
		artifacts.CollectFromPod(pod.Name, true, []string{s.name}, nil)
		notifier = artifacts
	}
	testCaseNotifier := NewTestCaseNotifier(notifier)

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
		log.Printf("cleanup: Deleting %s pod %s", s.name, s.config.As)
		if err := s.podClient.Pods(s.jobSpec.Namespace).Delete(s.config.As, nil); err != nil && !errors.IsNotFound(err) {
			log.Printf("error: Could not delete %s pod: %v", s.name, err)
		}
	}()

	pod, err = createOrRestartPod(s.podClient.Pods(s.jobSpec.Namespace), pod)
	if err != nil {
		return fmt.Errorf("failed to create or restart %s pod: %v", s.name, err)
	}

	defer func() {
		s.subTests = testCaseNotifier.SubTests(s.Description() + " - ")
	}()

	if err := waitForPodCompletion(s.podClient.Pods(s.jobSpec.Namespace), pod.Name, testCaseNotifier); err != nil {
		return fmt.Errorf("test %q failed: %v", pod.Name, err)
	}
	return nil
}

func (s *podStep) SubTests() []*junit.TestCase {
	return s.subTests
}

func (s *podStep) gatherArtifacts() bool {
	return len(s.config.ArtifactDir) > 0 && len(s.artifactDir) > 0
}

func (s *podStep) Done() (bool, error) {
	ready, err := isPodCompleted(s.podClient.Pods(s.jobSpec.Namespace), s.config.As)
	if err != nil {
		return false, fmt.Errorf("failed to determine if %s pod was completed: %v", s.name, err)
	}
	if !ready {
		return false, nil
	}
	return true, nil
}

func (s *podStep) Requires() []api.StepLink {
	if s.config.From.Name == api.PipelineImageStream {
		return []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReference(s.config.From.Tag))}
	}
	return []api.StepLink{api.ImagesReadyLink()}
}

func (s *podStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *podStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *podStep) Name() string { return s.config.As }

func (s *podStep) Description() string {
	return fmt.Sprintf("Run test %s", s.config.As)
}

func TestStep(config api.TestStepConfiguration, resources api.ResourceConfiguration, podClient PodClient, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return PodStep(
		"test",
		PodStepConfiguration{
			As:                 config.As,
			From:               api.ImageStreamTagReference{Name: api.PipelineImageStream, Tag: string(config.ContainerTestConfiguration.From)},
			Commands:           config.Commands,
			ArtifactDir:        config.ArtifactDir,
			Secret:             config.Secret,
			MemoryBackedVolume: config.ContainerTestConfiguration.MemoryBackedVolume,
		},
		resources,
		podClient,
		artifactDir,
		jobSpec,
	)
}

func PodStep(name string, config PodStepConfiguration, resources api.ResourceConfiguration, podClient PodClient, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return &podStep{
		name:        name,
		config:      config,
		resources:   resources,
		podClient:   podClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}

func (s *podStep) generatePodForStep(image string, containerResources coreapi.ResourceRequirements) (*coreapi.Pod, error) {
	pod := &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name: s.config.As,
			Labels: trimLabels(map[string]string{
				PersistsLabel:    "false",
				JobLabel:         s.jobSpec.Job,
				BuildIdLabel:     s.jobSpec.BuildId,
				ProwJobIdLabel:   s.jobSpec.ProwJobID,
				CreatedByCILabel: "true",
			}),
			Annotations: map[string]string{
				JobSpecAnnotation:                     s.jobSpec.RawSpec(),
				annotationContainersForSubTestResults: s.name,
			},
		},
		Spec: coreapi.PodSpec{
			ServiceAccountName: s.config.ServiceAccountName,
			RestartPolicy:      coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Image:                    image,
					Name:                     s.name,
					Command:                  []string{"/bin/sh", "-c", "#!/bin/sh\nset -eu\n" + s.config.Commands},
					Resources:                containerResources,
					TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
				},
			},
		},
	}

	if s.config.Secret.Name != "" {
		pod.Spec.Containers[0].VolumeMounts = getSecretVolumeMountFromSecret(s.config.Secret.MountPath)
		pod.Spec.Volumes = getVolumeFromSecret(s.config.Secret.Name)
	}

	if v := s.config.MemoryBackedVolume; v != nil {
		size, err := resource.ParseQuantity(v.Size)
		if err != nil {
			// validation should prevent this
			return nil, fmt.Errorf("invalid size for volume test %s: %v", s.config.As, v.Size)
		}
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      "memory-backed",
			MountPath: "/tmp/volume",
		})
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
			Name: "memory-backed",
			VolumeSource: coreapi.VolumeSource{
				EmptyDir: &coreapi.EmptyDirVolumeSource{
					Medium:    coreapi.StorageMediumMemory,
					SizeLimit: &size,
				},
			},
		})
	}

	return pod, nil
}

func getVolumeFromSecret(secretName string) []coreapi.Volume {
	return []coreapi.Volume{
		{
			Name: testSecretName,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		},
	}
}

func getSecretVolumeMountFromSecret(secretMountPath string) []coreapi.VolumeMount {
	if secretMountPath == "" {
		secretMountPath = testSecretDefaultPath
	}
	return []coreapi.VolumeMount{
		{
			Name:      testSecretName,
			ReadOnly:  true,
			MountPath: secretMountPath,
		},
	}
}
