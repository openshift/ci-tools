package steps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/decorate"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	testSecretVolumePrefix = "test-secret"
	testSecretDefaultPath  = "/usr/test-secrets"

	openshiftCIEnv = "OPENSHIFT_CI"
)

// CleanupCtx is used by steps when the primary context is cancelled.
// When we're cleaning up, we need a context to use for client calls, but we cannot
// use the normal context we have in steps, as that may be cancelled (and that would
// be why we're cleaning up in the first place).  This is intended only for
// internal usage of this package and its sub-packages.
var CleanupCtx = context.Background()

// PodStepConfiguration allows other steps to reuse the pod launching and monitoring
// behavior without reimplementing function. It also enforces conventions like naming,
// directory structure, and input image format. More sophisticated reuse of launching
// pods should use RunPod which is more limited.
type PodStepConfiguration struct {
	WaitFlags          util.WaitForPodFlag
	As                 string
	From               api.ImageStreamTagReference
	Commands           string
	Labels             map[string]string
	NodeName           string
	ServiceAccountName string
	Secrets            []*api.Secret
	MemoryBackedVolume *api.MemoryBackedVolume
	Clone              bool
	NodeArchitecture   api.NodeArchitecture
}

type GeneratePodOptions struct {
	Clone             bool
	PropagateExitCode bool
	NodeArchitecture  string
}

type podStep struct {
	name      string
	config    PodStepConfiguration
	resources api.ResourceConfiguration
	client    kubernetes.PodClient
	jobSpec   *api.JobSpec

	subTests []*junit.TestCase

	clusterClaim *api.ClusterClaim
}

func (s *podStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*podStep) Validate() error { return nil }

func (s *podStep) Run(ctx context.Context) error {
	return results.ForReason("running_pod").ForError(s.run(ctx))
}

func (s *podStep) run(ctx context.Context) error {
	if !util.IsBitSet(s.config.WaitFlags, util.SkipLogs) {
		logrus.Infof("Executing %s %s", s.name, s.config.As)
	}
	containerResources, err := ResourcesFor(s.resources.RequirementsForStep(s.config.As))
	if err != nil {
		return fmt.Errorf("unable to calculate %s pod resources for %s: %w", s.name, s.config.As, err)
	}

	if s.config.From.Namespace != "" {
		return errors.New("pod step does not support an image stream tag reference outside the namespace")
	}
	image := fmt.Sprintf("%s:%s", s.config.From.Name, s.config.From.Tag)

	pod, err := s.generatePodForStep(image, containerResources, s.config.Clone)
	if err != nil {
		return fmt.Errorf("pod step was invalid: %w", err)
	}
	testCaseNotifier := NewTestCaseNotifier(util.NopNotifier)

	if owner := s.jobSpec.Owner(); owner != nil {
		pod.OwnerReferences = append(pod.OwnerReferences, *owner)
	}

	go func() {
		<-ctx.Done()
		logrus.Infof("cleanup: Deleting %s pod %s", s.name, s.config.As)
		if err := s.client.Delete(CleanupCtx, &coreapi.Pod{ObjectMeta: meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: s.config.As}}); err != nil && !kerrors.IsNotFound(err) {
			logrus.WithError(err).Warnf("Could not delete %s pod.", s.name)
		}
	}()

	pod, err = util.CreateOrRestartPod(ctx, s.client, pod)
	if err != nil {
		return fmt.Errorf("failed to create or restart %s pod: %w", s.name, err)
	}

	defer func() {
		s.subTests = testCaseNotifier.SubTests(s.Description() + " - ")
	}()
	if _, err := util.WaitForPodCompletion(ctx, s.client, pod.Namespace, pod.Name, testCaseNotifier, s.config.WaitFlags); err != nil {
		return fmt.Errorf("%s %q failed: %w", s.name, pod.Name, err)
	}
	return nil
}

func (s *podStep) SubTests() []*junit.TestCase {
	return s.subTests
}

func (s *podStep) Requires() (ret []api.StepLink) {
	if s.config.From.Name == api.PipelineImageStream {
		ret = append(ret, api.InternalImageLink(api.PipelineImageStreamTagReference(s.config.From.Tag)))
		return
	}
	ret = append(ret, api.ImagesReadyLink())
	return
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

func (s *podStep) IsMultiArch() bool {
	return s.config.NodeArchitecture != "" && s.config.NodeArchitecture != api.NodeArchitectureAMD64
}
func (s *podStep) SetMultiArch(multiArch bool) {}

func TestStep(config api.TestStepConfiguration, resources api.ResourceConfiguration, client kubernetes.PodClient, jobSpec *api.JobSpec, nodeName string) api.Step {
	return PodStep(
		"test",
		PodStepConfiguration{
			As:                 config.As,
			From:               api.ImageStreamTagReference{Name: api.PipelineImageStream, Tag: string(config.ContainerTestConfiguration.From)},
			Commands:           config.Commands,
			NodeName:           nodeName,
			Secrets:            config.Secrets,
			MemoryBackedVolume: config.ContainerTestConfiguration.MemoryBackedVolume,
			Clone:              *config.ContainerTestConfiguration.Clone,
			NodeArchitecture:   config.NodeArchitecture,
		},
		resources,
		client,
		jobSpec,
		config.ClusterClaim,
	)
}

func PodStep(name string, config PodStepConfiguration, resources api.ResourceConfiguration, client kubernetes.PodClient, jobSpec *api.JobSpec, clusterClaim *api.ClusterClaim) api.Step {
	return &podStep{
		name:         name,
		config:       config,
		resources:    resources,
		client:       client,
		jobSpec:      jobSpec,
		clusterClaim: clusterClaim,
	}
}

func GenerateBasePod(
	jobSpec *api.JobSpec,
	baseLabels map[string]string,
	name string,
	nodeName string,
	containerName string,
	command []string,
	image string,
	containerResources coreapi.ResourceRequirements,
	artifactDir string,
	decorationConfig *v1.DecorationConfig,
	rawJobSpec string,
	secretsToCensor []coreapi.VolumeMount,
	generatePodOptions *GeneratePodOptions,
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
			Labels:    labelsFor(jobSpec, baseLabels, ""),
			Annotations: map[string]string{
				JobSpecAnnotation:                     jobSpec.RawSpec(),
				annotationContainersForSubTestResults: containerName,
			},
		},
		Spec: coreapi.PodSpec{
			NodeName:      nodeName,
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

	// FIXME: Fix this workaround upstream and the delete this code as soon as possible
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, []coreapi.EnvVar{
		{Name: "GIT_CONFIG_COUNT", Value: "1"},
		{Name: "GIT_CONFIG_KEY_0", Value: "safe.directory"},
		{Name: "GIT_CONFIG_VALUE_0", Value: "*"},
	}...)

	if generatePodOptions != nil && generatePodOptions.NodeArchitecture != "" {
		pod.Spec.NodeSelector = map[string]string{"kubernetes.io/arch": generatePodOptions.NodeArchitecture}
	}

	artifactDir = fmt.Sprintf("artifacts/%s", artifactDir)
	if err := addPodUtils(pod, artifactDir, decorationConfig, rawJobSpec, secretsToCensor, generatePodOptions, jobSpec); err != nil {
		return nil, fmt.Errorf("failed to decorate pod: %w", err)
	}
	return pod, nil
}

func (s *podStep) generatePodForStep(image string, containerResources coreapi.ResourceRequirements, clone bool) (*coreapi.Pod, error) {
	var secretVolumes []coreapi.Volume
	var secretVolumeMounts []coreapi.VolumeMount
	for i, secret := range s.config.Secrets {
		secretVolumeMounts = append(secretVolumeMounts, getSecretVolumeMountFromSecret(secret.MountPath, i)...)
		secretVolumes = append(secretVolumes, getVolumeFromSecret(secret.Name, i)...)
	}
	if s.clusterClaim != nil {
		secretVolumeMounts = append(secretVolumeMounts, []coreapi.VolumeMount{
			{
				Name:      NamePerTest(api.HiveAdminKubeconfigSecret, s.config.As),
				ReadOnly:  true,
				MountPath: filepath.Join(testSecretDefaultPath, NamePerTest(api.HiveAdminKubeconfigSecret, s.config.As)),
			},
			{
				Name:      NamePerTest(api.HiveAdminPasswordSecret, s.config.As),
				ReadOnly:  true,
				MountPath: filepath.Join(testSecretDefaultPath, NamePerTest(api.HiveAdminPasswordSecret, s.config.As)),
			},
		}...)
		secretVolumes = append(secretVolumes, []coreapi.Volume{
			{
				Name: NamePerTest(api.HiveAdminKubeconfigSecret, s.config.As),
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: NamePerTest(api.HiveAdminKubeconfigSecret, s.config.As),
					},
				},
			},
			{
				Name: NamePerTest(api.HiveAdminPasswordSecret, s.config.As),
				VolumeSource: coreapi.VolumeSource{
					Secret: &coreapi.SecretVolumeSource{
						SecretName: NamePerTest(api.HiveAdminPasswordSecret, s.config.As),
					},
				},
			},
		}...)
	}

	artifactDir := s.name
	pod, err := GenerateBasePod(s.jobSpec, s.config.Labels, s.config.As,
		s.config.NodeName, s.name, []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + s.config.Commands},
		image, containerResources, artifactDir, s.jobSpec.DecorationConfig, s.jobSpec.RawSpec(),
		secretVolumeMounts, &GeneratePodOptions{Clone: clone, PropagateExitCode: false, NodeArchitecture: string(s.config.NodeArchitecture)})
	if err != nil {
		return nil, err
	}
	pod.Spec.ServiceAccountName = s.config.ServiceAccountName
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, secretVolumeMounts...)
	if s.clusterClaim != nil {
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "KUBECONFIG", Value: filepath.Join(filepath.Join(testSecretDefaultPath, NamePerTest(api.HiveAdminKubeconfigSecret, s.config.As)), api.HiveAdminKubeconfigSecretKey)},
			{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(filepath.Join(testSecretDefaultPath, NamePerTest(api.HiveAdminPasswordSecret, s.config.As)), api.HiveAdminPasswordSecretKey)},
		}...)
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, secretVolumes...)

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
func RunPod(ctx context.Context, podClient kubernetes.PodClient, pod *coreapi.Pod) (*coreapi.Pod, error) {
	pod, err := util.CreateOrRestartPod(ctx, podClient, pod)
	if err != nil {
		return pod, err
	}
	return util.WaitForPodCompletion(ctx, podClient, pod.Namespace, pod.Name, nil, util.SkipLogs)
}
