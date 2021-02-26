package steps

import (
	"context"
	"fmt"
	"log"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
)

const (
	testSecretVolumePrefix = "test-secret"
	testSecretDefaultPath  = "/usr/test-secrets"
	homeVolumeName         = "home"

	openshiftCIEnv = "OPENSHIFT_CI"
)

// when we're cleaning up, we need a context to use for client calls, but we cannot
// use the normal context we have in steps, as that may be cancelled (and that would
// be why we're cleaning up in the first place).
var cleanupCtx = context.Background()

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
	ServiceAccountName string
	Secrets            []*api.Secret
	MemoryBackedVolume *api.MemoryBackedVolume
}

type podStep struct {
	name        string
	config      PodStepConfiguration
	resources   api.ResourceConfiguration
	client      PodClient
	artifactDir string
	jobSpec     *api.JobSpec

	subTests []*junit.TestCase
}

func (s *podStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*podStep) Validate() error { return nil }

func (s *podStep) Run(ctx context.Context) error {
	return results.ForReason("running_pod").ForError(s.run(ctx))
}

func (s *podStep) run(ctx context.Context) error {
	if !s.config.SkipLogs {
		log.Printf("Executing %s %s", s.name, s.config.As)
	}
	containerResources, err := resourcesFor(s.resources.RequirementsForStep(s.config.As))
	if err != nil {
		return fmt.Errorf("unable to calculate %s pod resources for %s: %w", s.name, s.config.As, err)
	}

	if len(s.config.From.Namespace) > 0 {
		return fmt.Errorf("pod step does not support an image stream tag reference outside the namespace")
	}
	image := fmt.Sprintf("%s:%s", s.config.From.Name, s.config.From.Tag)

	pod, err := s.generatePodForStep(image, containerResources)
	if err != nil {
		return fmt.Errorf("pod step was invalid: %w", err)
	}

	testCaseNotifier := NewTestCaseNotifier(NopNotifier)

	if owner := s.jobSpec.Owner(); owner != nil {
		pod.OwnerReferences = append(pod.OwnerReferences, *owner)
	}

	go func() {
		<-ctx.Done()
		log.Printf("cleanup: Deleting %s pod %s", s.name, s.config.As)
		if err := s.client.Delete(cleanupCtx, &coreapi.Pod{ObjectMeta: meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: s.config.As}}); err != nil && !kerrors.IsNotFound(err) {
			log.Printf("error: Could not delete %s pod: %v", s.name, err)
		}
	}()

	pod, err = createOrRestartPod(s.client, pod)
	if err != nil {
		return fmt.Errorf("failed to create or restart %s pod: %w", s.name, err)
	}

	defer func() {
		s.subTests = testCaseNotifier.SubTests(s.Description() + " - ")
	}()

	if _, err := waitForPodCompletion(ctx, s.client, pod.Namespace, pod.Name, testCaseNotifier, s.config.SkipLogs); err != nil {
		return fmt.Errorf("%s %q failed: %w", s.name, pod.Name, err)
	}
	return nil
}

func (s *podStep) SubTests() []*junit.TestCase {
	return s.subTests
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

func (s *podStep) Provides() api.ParameterMap {
	return nil
}

func (s *podStep) Name() string { return s.config.As }

func (s *podStep) Description() string {
	return fmt.Sprintf("Run test %s", s.config.As)
}

func (s *podStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func TestStep(config api.TestStepConfiguration, resources api.ResourceConfiguration, client PodClient, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return PodStep(
		"test",
		PodStepConfiguration{
			As:                 config.As,
			From:               api.ImageStreamTagReference{Name: api.PipelineImageStream, Tag: string(config.ContainerTestConfiguration.From)},
			Commands:           config.Commands,
			Secrets:            config.Secrets,
			MemoryBackedVolume: config.ContainerTestConfiguration.MemoryBackedVolume,
		},
		resources,
		client,
		artifactDir,
		jobSpec,
	)
}

func PodStep(name string, config PodStepConfiguration, resources api.ResourceConfiguration, client PodClient, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return &podStep{
		name:        name,
		config:      config,
		resources:   resources,
		client:      client,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}

func generateBasePod(
	jobSpec *api.JobSpec,
	name string,
	containerName string,
	command []string,
	image string,
	containerResources coreapi.ResourceRequirements,
	artifactDir string,
	decorationConfig *v1.DecorationConfig,
	rawJobSpec string,
) (*coreapi.Pod, error) {
	envMap, err := downwardapi.EnvForSpec(jobSpec.JobSpec)
	envMap[openshiftCIEnv] = "true"
	if err != nil {
		return nil, err
	}
	pod := &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Namespace: jobSpec.Namespace(),
			Name:      name,
			Labels:    defaultPodLabels(jobSpec),
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
		// When using the old-school artifacts upload, we can be declarative as to
		// where the artifacts exist. In the pod-utils approach, we instead declare
		// what the subdirectory should be for the upload. The subdirectory allows
		// us to upload to where the old-school approach would have put things.
		artifactDir = fmt.Sprintf("artifacts/%s", artifactDir)
		if err := addPodUtils(pod, artifactDir, decorationConfig, rawJobSpec); err != nil {
			return nil, fmt.Errorf("failed to decorate pod: %w", err)
		}
	}
	return pod, nil
}

func (s *podStep) generatePodForStep(image string, containerResources coreapi.ResourceRequirements) (*coreapi.Pod, error) {
	artifactDir := s.name
	pod, err := generateBasePod(s.jobSpec, s.config.As, s.name, []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + s.config.Commands}, image, containerResources, artifactDir, s.jobSpec.DecorationConfig, s.jobSpec.RawSpec())
	if err != nil {
		return nil, err
	}
	pod.Spec.ServiceAccountName = s.config.ServiceAccountName
	container := &pod.Spec.Containers[0]
	for i, secret := range s.config.Secrets {
		container.VolumeMounts = append(container.VolumeMounts, getSecretVolumeMountFromSecret(secret.MountPath, i)...)
		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolumeFromSecret(secret.Name, i)...)
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

func getVolumeFromSecret(secretName string, secretIndex int) []coreapi.Volume {
	volumeName := testSecretVolumePrefix
	if secretIndex > 0 {
		// Preserve mount volume name to preserve legacy in case anything cares.
		volumeName = fmt.Sprintf("%s-%d", testSecretVolumePrefix, secretIndex+1)
	}
	return []coreapi.Volume{
		{
			Name: volumeName,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		},
	}
}

func getSecretVolumeMountFromSecret(secretMountPath string, secretIndex int) []coreapi.VolumeMount {
	if secretMountPath == "" {
		secretMountPath = testSecretDefaultPath
		if secretIndex > 0 {
			// Preserve testSecretDefaultPath for the first entry to preserve legacy
			// location.
			secretMountPath = fmt.Sprintf("%s-%d", testSecretDefaultPath, secretIndex+1)
		}
	}
	volumeName := testSecretVolumePrefix
	if secretIndex > 0 {
		// Preserve mount volume name to preserve legacy in case anything cares.
		volumeName = fmt.Sprintf("%s-%d", testSecretVolumePrefix, secretIndex+1)
	}
	return []coreapi.VolumeMount{
		{
			Name:      volumeName,
			ReadOnly:  true,
			MountPath: secretMountPath,
		},
	}
}

// RunPod may be used to run a pod to completion. Provides a simpler interface than
// PodStep and is intended for other steps that may need to run transient actions.
// This pod will not be able to gather artifacts, nor will it report log messages
// unless it fails.
func RunPod(ctx context.Context, podClient PodClient, pod *coreapi.Pod) (*coreapi.Pod, error) {
	pod, err := createOrRestartPod(podClient, pod)
	if err != nil {
		return pod, err
	}
	return waitForPodCompletion(ctx, podClient, pod.Namespace, pod.Name, nil, true)
}
