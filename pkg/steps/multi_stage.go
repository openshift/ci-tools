package steps

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
)

const (
	multiStageTestLabel     = "ci.openshift.io/multi-stage-test"
	clusterProfileMountPath = "/var/run/secrets/ci.openshift.io/cluster-profile"
	secretMountPath         = "/var/run/secrets/ci.openshift.io/multi-stage"
	secretMountEnv          = "SHARED_DIR"
	clusterProfileMountEnv  = "CLUSTER_PROFILE_DIR"
)

type multiStageTestStep struct {
	dry             bool
	logger          *DryLogger
	name            string
	releaseInitial  string
	releaseLatest   string
	profile         api.ClusterProfile
	config          *api.ReleaseBuildConfiguration
	params          api.Parameters
	podClient       PodClient
	secretClient    coreclientset.SecretsGetter
	saClient        coreclientset.ServiceAccountsGetter
	rbacClient      rbacclientset.RbacV1Interface
	artifactDir     string
	jobSpec         *api.JobSpec
	pre, test, post []api.LiteralTestStep
	subTests        []*junit.TestCase
}

func MultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	podClient PodClient,
	secretClient coreclientset.SecretsGetter,
	saClient coreclientset.ServiceAccountsGetter,
	rbacClient rbacclientset.RbacV1Interface,
	artifactDir string,
	jobSpec *api.JobSpec,
	logger *DryLogger,
) api.Step {
	return newMultiStageTestStep(testConfig, config, params, podClient, secretClient, saClient, rbacClient, artifactDir, jobSpec, logger)
}

func newMultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	podClient PodClient,
	secretClient coreclientset.SecretsGetter,
	saClient coreclientset.ServiceAccountsGetter,
	rbacClient rbacclientset.RbacV1Interface,
	artifactDir string,
	jobSpec *api.JobSpec,
	logger *DryLogger,
) *multiStageTestStep {
	if artifactDir != "" {
		artifactDir = filepath.Join(artifactDir, testConfig.As)
	}
	return &multiStageTestStep{
		logger:       logger,
		name:         testConfig.As,
		profile:      testConfig.MultiStageTestConfigurationLiteral.ClusterProfile,
		config:       config,
		params:       params,
		podClient:    podClient,
		secretClient: secretClient,
		saClient:     saClient,
		rbacClient:   rbacClient,
		artifactDir:  artifactDir,
		jobSpec:      jobSpec,
		pre:          testConfig.MultiStageTestConfigurationLiteral.Pre,
		test:         testConfig.MultiStageTestConfigurationLiteral.Test,
		post:         testConfig.MultiStageTestConfigurationLiteral.Post,
	}
}

func (s *multiStageTestStep) profileSecretName() string {
	return s.name + "-cluster-profile"
}

func (s *multiStageTestStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *multiStageTestStep) Run(ctx context.Context, dry bool) error {
	s.dry = dry
	if s.profile != "" {
		if !dry {
			secret := s.profileSecretName()
			if _, err := s.secretClient.Secrets(s.jobSpec.Namespace).Get(secret, meta.GetOptions{}); err != nil {
				return fmt.Errorf("could not find secret %q: %v", secret, err)
			}
		}
		var err error
		if s.releaseInitial, err = s.params.Get("RELEASE_IMAGE_INITIAL"); err != nil {
			return err
		}
		if s.releaseLatest, err = s.params.Get("RELEASE_IMAGE_LATEST"); err != nil {
			return err
		}
	}
	if err := s.createSecret(); err != nil {
		return fmt.Errorf("failed to create secret: %v", err)
	}
	if err := s.setupRBAC(); err != nil {
		return fmt.Errorf("failed to create RBAC objects: %v", err)
	}
	var errs []error
	if err := s.runSteps(ctx, s.pre, true); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %v", s.name, err))
	} else if err := s.runSteps(ctx, s.test, true); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %v", s.name, err))
	}
	if err := s.runSteps(context.Background(), s.post, false); err != nil {
		errs = append(errs, fmt.Errorf("%q post steps failed: %v", s.name, err))
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) Name() string { return s.name }
func (s *multiStageTestStep) Description() string {
	return fmt.Sprintf("Run multi-stage test %s", s.name)
}

func (s *multiStageTestStep) Requires() (ret []api.StepLink) {
	var needsRelease bool
	for _, step := range append(append(s.pre, s.test...), s.post...) {
		if s.config.IsPipelineImage(step.From) || s.config.BuildsImage(step.From) {
			ret = append(ret, api.InternalImageLink(api.PipelineImageStreamTagReference(step.From)))
		} else {
			needsRelease = true
		}
	}
	if needsRelease {
		ret = append(ret, api.ReleaseImagesLink())
	}
	if s.profile != "" {
		ret = append(ret, s.params.Links("RELEASE_IMAGE_INITIAL")...)
		ret = append(ret, s.params.Links("RELEASE_IMAGE_LATEST")...)
	}
	return
}

func (s *multiStageTestStep) Creates() []api.StepLink { return nil }
func (s *multiStageTestStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}
func (s *multiStageTestStep) SubTests() []*junit.TestCase { return s.subTests }

func (s *multiStageTestStep) setupRBAC() error {
	labels := map[string]string{multiStageTestLabel: s.name}
	m := meta.ObjectMeta{Name: s.name, Labels: labels}
	sa := &coreapi.ServiceAccount{ObjectMeta: m}
	role := &rbacapi.Role{
		ObjectMeta: m,
		Rules: []rbacapi.PolicyRule{{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings"},
			Verbs:     []string{"create", "list"},
		}, {
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: []string{s.name},
			Verbs:         []string{"update"},
		}, {
			APIGroups: []string{"", "image.openshift.io"},
			Resources: []string{"imagestreams/layers"},
			Verbs:     []string{"get"},
		}},
	}
	subj := []rbacapi.Subject{{Kind: "ServiceAccount", Name: s.name}}
	binding := &rbacapi.RoleBinding{
		ObjectMeta: m,
		RoleRef:    rbacapi.RoleRef{Kind: "Role", Name: s.name},
		Subjects:   subj,
	}
	if s.dry {
		s.logger.AddObject(sa.DeepCopyObject())
		s.logger.AddObject(role.DeepCopyObject())
		s.logger.AddObject(binding.DeepCopyObject())
		return nil
	}
	check := func(err error) bool {
		return err == nil || errors.IsAlreadyExists(err)
	}
	if _, err := s.saClient.ServiceAccounts(s.jobSpec.Namespace).Create(sa); !check(err) {
		return err
	}
	if _, err := s.rbacClient.Roles(s.jobSpec.Namespace).Create(role); !check(err) {
		return err
	}
	if _, err := s.rbacClient.RoleBindings(s.jobSpec.Namespace).Create(binding); !check(err) {
		return err
	}
	return nil
}

func (s *multiStageTestStep) createSecret() error {
	log.Printf("Creating multi-stage test secret %q", s.name)
	secret := coreapi.Secret{ObjectMeta: meta.ObjectMeta{Name: s.name}}
	if s.dry {
		s.logger.AddObject(secret.DeepCopyObject())
		return nil
	}
	client := s.secretClient.Secrets(s.jobSpec.Namespace)
	if err := client.Delete(s.name, &meta.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("cannot delete secret %q: %v", s.name, err)
	}
	_, err := client.Create(&secret)
	return err
}

func (s *multiStageTestStep) runSteps(ctx context.Context, steps []api.LiteralTestStep, shortCircuit bool) error {
	pods, err := s.generatePods(steps)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.runPods(ctx, pods, shortCircuit); err != nil {
		errs = append(errs, err)
	}
	select {
	case <-ctx.Done():
		log.Printf("cleanup: Deleting pods with label %s=%s", multiStageTestLabel, s.name)
		if !s.dry {
			if err := deletePods(s.podClient.Pods(s.jobSpec.Namespace), s.name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete pods with label %s=%s: %v", multiStageTestLabel, s.name, err))
			}
		}
		errs = append(errs, fmt.Errorf("cancelled"))
	default:
		break
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) generatePods(steps []api.LiteralTestStep) ([]coreapi.Pod, error) {
	var ret []coreapi.Pod
	var errs []error
	for _, step := range steps {
		image := step.From
		if s.config.IsPipelineImage(image) || s.config.BuildsImage(image) {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, image)
		}
		resources, err := resourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		pod, err := generateBasePod(s.jobSpec, name, step.As, []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + step.Commands}, image, resources, step.ArtifactDir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		delete(pod.Labels, ProwJobIdLabel)
		pod.Annotations[annotationSaveContainerLogs] = "true"
		pod.Labels[multiStageTestLabel] = s.name
		pod.Spec.ServiceAccountName = s.name
		addSecretWrapper(pod)
		container := &pod.Spec.Containers[0]
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "NAMESPACE", Value: s.jobSpec.Namespace},
			{Name: "JOB_NAME_SAFE", Value: strings.Replace(s.name, "_", "-", -1)},
			{Name: "JOB_NAME_HASH", Value: s.jobSpec.JobNameHash()},
		}...)
		if owner := s.jobSpec.Owner(); owner != nil {
			pod.OwnerReferences = append(pod.OwnerReferences, *owner)
		}
		if s.profile != "" {
			addProfile(s.profileSecretName(), s.profile, pod)
			container.Env = append(container.Env, []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: filepath.Join(secretMountPath, "kubeconfig")},
				{Name: "RELEASE_IMAGE_INITIAL", Value: s.releaseInitial},
				{Name: "RELEASE_IMAGE_LATEST", Value: s.releaseLatest},
			}...)
		}
		addSecret(s.name, pod)
		ret = append(ret, *pod)
	}
	return ret, utilerrors.NewAggregate(errs)
}

func addSecretWrapper(pod *coreapi.Pod) {
	volume := "secret-wrapper"
	dir := "/tmp/secret-wrapper"
	bin := filepath.Join(dir, "secret-wrapper")
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volume,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	mount := coreapi.VolumeMount{Name: volume, MountPath: dir}
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Image:                    "registry.svc.ci.openshift.org/ci/secret-wrapper:latest",
		Name:                     "cp-secret-wrapper",
		Command:                  []string{"cp"},
		Args:                     []string{"/bin/secret-wrapper", bin},
		VolumeMounts:             []coreapi.VolumeMount{mount},
		TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
	})
	container := &pod.Spec.Containers[0]
	container.Args = append([]string{}, append(container.Command, container.Args...)...)
	container.Command = []string{bin}
	container.VolumeMounts = append(container.VolumeMounts, mount)
}

func addSecret(secret string, pod *coreapi.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: secret,
		VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{SecretName: secret},
		},
	})
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
		Name:      secret,
		MountPath: secretMountPath,
	})
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, coreapi.EnvVar{
		Name:  secretMountEnv,
		Value: secretMountPath,
	})
}

func addProfile(name string, profile api.ClusterProfile, pod *coreapi.Pod) {
	volumeName := "cluster-profile"
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{
				SecretName: name,
			},
		},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      volumeName,
		MountPath: clusterProfileMountPath,
	})
	container.Env = append(container.Env, []coreapi.EnvVar{{
		Name:  "CLUSTER_TYPE",
		Value: profile.ClusterType(),
	}, {
		Name:  clusterProfileMountEnv,
		Value: clusterProfileMountPath,
	}}...)
}

func (s *multiStageTestStep) runPods(ctx context.Context, pods []coreapi.Pod, shortCircuit bool) error {
	done := ctx.Done()
	var errs []error
	for _, pod := range pods {
		log.Printf("Executing %q", pod.Name)
		var notifier ContainerNotifier = NopNotifier
		for _, c := range pod.Spec.Containers {
			if c.Name == "artifacts" {
				container := pod.Spec.Containers[0].Name
				artifacts := NewArtifactWorker(s.podClient, filepath.Join(s.artifactDir, container), s.jobSpec.Namespace)
				artifacts.CollectFromPod(pod.Name, []string{container}, nil)
				notifier = artifacts
				break
			}
		}
		err := s.runPod(ctx, &pod, NewTestCaseNotifier(notifier))
		select {
		case <-done:
			notifier.Cancel()
		default:
		}
		if err != nil {
			errs = append(errs, err)
			if shortCircuit {
				break
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) runPod(ctx context.Context, pod *coreapi.Pod, notifier *TestCaseNotifier) error {
	if s.dry {
		s.logger.AddObject(pod.DeepCopyObject())
		return nil
	}
	if _, err := createOrRestartPod(s.podClient.Pods(s.jobSpec.Namespace), pod); err != nil {
		return fmt.Errorf("failed to create or restart %q pod: %v", pod.Name, err)
	}
	if err := waitForPodCompletion(ctx, s.podClient.Pods(s.jobSpec.Namespace), pod.Name, notifier, false); err != nil {
		return fmt.Errorf("%q pod %q failed: %v", s.name, pod.Name, err)
	}
	s.subTests = append(s.subTests, notifier.SubTests(fmt.Sprintf("%s - %s ", s.Description(), pod.Name))...)
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
