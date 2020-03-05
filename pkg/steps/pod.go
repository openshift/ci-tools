package steps

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
)

const testSecretName = "test-secret"
const testSecretDefaultPath = "/usr/test-secrets"

// PodStepConfiguration allows other steps to reuse the pod launching and monitoring
// behavior without reimplementing function. It also enforces conventions like naming,
// directory structure, and input image format. More sophisticated reuse of launching
// pods should use RunPod which is more limited.
type PodStepConfiguration struct {
	// SkipLogs instructs the step to omit informational logs, such as when the pod is
	// part of a larger step like release creation where displaying pod specific info
	// is confusing to an end user. Failure logs are still printed.
	SkipLogs           bool
	As                 string
	From               api.ImageStreamTagReference
	Commands           string
	ArtifactDir        string
	ServiceAccountName string
	Secrets            []*api.Secret
	MemoryBackedVolume *api.MemoryBackedVolume
}

type podStep struct {
	name        string
	config      PodStepConfiguration
	resources   api.ResourceConfiguration
	podClient   PodClient
	artifactDir string
	jobSpec     *api.JobSpec
	dryLogger   *DryLogger

	subTests []*junit.TestCase
}

func (s *podStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *podStep) Run(ctx context.Context, dry bool) error {
	if !s.config.SkipLogs {
		log.Printf("Executing %s %s", s.name, s.config.As)
	}
	containerResources, err := resourcesFor(s.resources.RequirementsForStep(s.config.As))
	if err != nil {
		return fmt.Errorf("unable to calculate %s pod resources for %s: %s", s.name, s.config.As, err)
	}

	if len(s.config.From.Namespace) > 0 {
		return fmt.Errorf("pod step does not support an image stream tag reference outside the namespace")
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
		artifacts.CollectFromPod(pod.Name, []string{s.name}, nil)
		notifier = artifacts
	}
	testCaseNotifier := NewTestCaseNotifier(notifier)

	if owner := s.jobSpec.Owner(); owner != nil {
		pod.OwnerReferences = append(pod.OwnerReferences, *owner)
	}

	if dry {
		s.dryLogger.AddObject(pod.DeepCopyObject())
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

	if err := waitForPodCompletion(context.TODO(), s.podClient.Pods(s.jobSpec.Namespace), pod.Name, testCaseNotifier, s.config.SkipLogs); err != nil {
		return fmt.Errorf("%s %q failed: %v", s.name, pod.Name, err)
	}
	return nil
}

func (s *podStep) SubTests() []*junit.TestCase {
	return s.subTests
}

func (s *podStep) gatherArtifacts() bool {
	return len(s.config.ArtifactDir) > 0 && len(s.artifactDir) > 0
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

func TestStep(config api.TestStepConfiguration, resources api.ResourceConfiguration, podClient PodClient, artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger) api.Step {
	return PodStep(
		"test",
		PodStepConfiguration{
			As:                 config.As,
			From:               api.ImageStreamTagReference{Name: api.PipelineImageStream, Tag: string(config.ContainerTestConfiguration.From)},
			Commands:           config.Commands,
			ArtifactDir:        config.ArtifactDir,
			Secrets:            config.Secrets,
			MemoryBackedVolume: config.ContainerTestConfiguration.MemoryBackedVolume,
		},
		resources,
		podClient,
		artifactDir,
		jobSpec,
		dryLogger,
	)
}

func PodStep(name string, config PodStepConfiguration, resources api.ResourceConfiguration, podClient PodClient, artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger) api.Step {
	return &podStep{
		name:        name,
		config:      config,
		resources:   resources,
		podClient:   podClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		dryLogger:   dryLogger,
	}
}

func generateBasePod(
	jobSpec *api.JobSpec,
	podName, containerName string,
	command []string,
	image string,
	containerResources coreapi.ResourceRequirements,
	artifactDir string,
) (*coreapi.Pod, error) {
	envMap, err := downwardapi.EnvForSpec(jobSpec.JobSpec)
	if err != nil {
		return nil, err
	}
	pod := &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:   podName,
			Labels: defaultPodLabels(jobSpec),
			Annotations: map[string]string{
				JobSpecAnnotation:                     jobSpec.RawSpec(),
				annotationContainersForSubTestResults: containerName,
			},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Image:                    image,
					Env:                      decorate.KubeEnv(envMap),
					Name:                     containerName,
					Command:                  command,
					Resources:                containerResources,
					TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
				},
			},
		},
	}
	if artifactDir != "" {
		addArtifacts(pod, artifactDir)
	}
	return pod, nil
}

func (s *podStep) generatePodForStep(image string, containerResources coreapi.ResourceRequirements) (*coreapi.Pod, error) {
	pod, err := generateBasePod(s.jobSpec, s.config.As, s.name, []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + s.config.Commands}, image, containerResources, s.config.ArtifactDir)
	if err != nil {
		return nil, err
	}
	pod.Spec.ServiceAccountName = s.config.ServiceAccountName
	container := &pod.Spec.Containers[0]
	for _, secret := range s.config.Secrets {
		container.VolumeMounts = append(container.VolumeMounts, getSecretVolumeMountFromSecret(secret.MountPath)...)
		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolumeFromSecret(secret.Name)...)
	}

	if v := s.config.MemoryBackedVolume; v != nil {
		size, err := resource.ParseQuantity(v.Size)
		if err != nil {
			// validation should prevent this
			return nil, fmt.Errorf("invalid size for volume test %s: %v", s.config.As, v.Size)
		}
		container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
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

// RunPod may be used to run a pod to completion. Provides a simpler interface than
// PodStep and is intended for other steps that may need to run transient actions.
// This pod will not be able to gather artifacts, nor will it report log messages
// unless it fails.
func RunPod(ctx context.Context, podClient PodClient, pod *coreapi.Pod) error {
	pod, err := createOrRestartPod(podClient.Pods(pod.Namespace), pod)
	if err != nil {
		return err
	}
	return waitForPodCompletion(ctx, podClient.Pods(pod.Namespace), pod.Name, nil, true)
}
