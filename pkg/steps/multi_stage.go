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
	"github.com/openshift/ci-tools/pkg/results"
)

const (
	// MultiStageTestLabel is the label we use to mark a pod as part of a multi-stage test
	MultiStageTestLabel = "ci.openshift.io/multi-stage-test"
	// ClusterProfileMountPath is where we mount the cluster profile in a pod
	ClusterProfileMountPath = "/var/run/secrets/ci.openshift.io/cluster-profile"
	// SecretMountPath is where we mount the shared dir secret
	SecretMountPath = "/var/run/secrets/ci.openshift.io/multi-stage"
	// SecretMountEnv is the env we use to expose the shared dir
	SecretMountEnv = "SHARED_DIR"
	// ClusterProfileMountEnv is the env we use to expose the cluster profile dir
	ClusterProfileMountEnv = "CLUSTER_PROFILE_DIR"
	// InitialReleaseEnv is the environment we use to expose the initial payload
	InitialReleaseEnv = "RELEASE_IMAGE_INITIAL"
	// LatestReleaseEnv is the environment we use to expose the latest payload
	LatestReleaseEnv = "RELEASE_IMAGE_LATEST"
	// ImageFormatEnv is the environment we use to hold the base pull spec
	ImageFormatEnv = "IMAGE_FORMAT"
)

var envForProfile = []string{InitialReleaseEnv, LatestReleaseEnv, leaseEnv, ImageFormatEnv}

type multiStageTestStep struct {
	dry     bool
	logger  *DryLogger
	name    string
	profile api.ClusterProfile
	config  *api.ReleaseBuildConfiguration
	// params exposes getters for variables created by other steps
	params          api.Parameters
	env             api.TestEnvironment
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
	ms := testConfig.MultiStageTestConfigurationLiteral
	return &multiStageTestStep{
		logger:       logger,
		name:         testConfig.As,
		profile:      ms.ClusterProfile,
		config:       config,
		params:       params,
		env:          ms.Environment,
		podClient:    podClient,
		secretClient: secretClient,
		saClient:     saClient,
		rbacClient:   rbacClient,
		artifactDir:  artifactDir,
		jobSpec:      jobSpec,
		pre:          ms.Pre,
		test:         ms.Test,
		post:         ms.Post,
	}
}

func (s *multiStageTestStep) profileSecretName() string {
	return s.name + "-cluster-profile"
}

func (s *multiStageTestStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *multiStageTestStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("executing_multi_stage_test").ForError(s.run(ctx, dry))
}

func (s *multiStageTestStep) run(ctx context.Context, dry bool) error {
	s.dry = dry
	var env []coreapi.EnvVar
	if s.profile != "" {
		if !dry {
			secret := s.profileSecretName()
			if _, err := s.secretClient.Secrets(s.jobSpec.Namespace()).Get(secret, meta.GetOptions{}); err != nil {
				return fmt.Errorf("could not find secret %q: %v", secret, err)
			}
		}
		for _, e := range envForProfile {
			val, err := s.params.Get(e)
			if err != nil {
				return err
			}
			env = append(env, coreapi.EnvVar{Name: e, Value: val})
		}
		if optionalOperator, err := resolveOptionalOperator(s.params); err != nil {
			return err
		} else if optionalOperator != nil {
			env = append(env, optionalOperator.asEnv()...)
		}
	}
	if err := s.createSecret(); err != nil {
		return fmt.Errorf("failed to create secret: %v", err)
	}
	if err := s.createCredentials(); err != nil {
		return fmt.Errorf("failed to create credentials: %v", err)
	}
	if err := s.setupRBAC(); err != nil {
		return fmt.Errorf("failed to create RBAC objects: %v", err)
	}
	var errs []error
	if err := s.runSteps(ctx, s.pre, env, true); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %v", s.name, err))
	} else if err := s.runSteps(ctx, s.test, env, true); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %v", s.name, err))
	}
	if err := s.runSteps(context.Background(), s.post, env, false); err != nil {
		errs = append(errs, fmt.Errorf("%q post steps failed: %v", s.name, err))
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) Name() string { return s.name }
func (s *multiStageTestStep) Description() string {
	return fmt.Sprintf("Run multi-stage test %s", s.name)
}

func (s *multiStageTestStep) Requires() (ret []api.StepLink) {
	var needsReleaseImage, needsReleasePayload bool
	internalLinks := map[api.PipelineImageStreamTagReference]struct{}{}
	for _, step := range append(append(s.pre, s.test...), s.post...) {
		if s.config.IsPipelineImage(step.From) || s.config.BuildsImage(step.From) {
			internalLinks[api.PipelineImageStreamTagReference(step.From)] = struct{}{}
		} else {
			needsReleaseImage = true
		}

		if link, ok := step.FromImageTag(); ok {
			internalLinks[link] = struct{}{}
		}
	}
	for link := range internalLinks {
		ret = append(ret, api.InternalImageLink(link))
	}
	if s.profile != "" {
		needsReleasePayload = true
		for _, env := range envForProfile {
			ret = append(ret, s.params.Links(env)...)
		}
	}
	if needsReleaseImage && !needsReleasePayload {
		ret = append(ret, api.StableImagesLink(api.LatestStableName))
	}
	return
}

func (s *multiStageTestStep) Creates() []api.StepLink { return nil }
func (s *multiStageTestStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}
func (s *multiStageTestStep) SubTests() []*junit.TestCase { return s.subTests }

func (s *multiStageTestStep) setupRBAC() error {
	labels := map[string]string{MultiStageTestLabel: s.name}
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
			Verbs:         []string{"get", "update"},
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
	if _, err := s.saClient.ServiceAccounts(s.jobSpec.Namespace()).Create(sa); !check(err) {
		return err
	}
	if _, err := s.rbacClient.Roles(s.jobSpec.Namespace()).Create(role); !check(err) {
		return err
	}
	if _, err := s.rbacClient.RoleBindings(s.jobSpec.Namespace()).Create(binding); !check(err) {
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
	client := s.secretClient.Secrets(s.jobSpec.Namespace())
	if err := client.Delete(s.name, &meta.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("cannot delete secret %q: %v", s.name, err)
	}
	_, err := client.Create(&secret)
	return err
}

func (s *multiStageTestStep) createCredentials() error {
	log.Printf("Creating multi-stage test credentials for %q", s.name)
	toCreate := map[string]*coreapi.Secret{}
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		for _, credential := range step.Credentials {
			// we don't want secrets imported from separate namespaces to collide
			// but we want to keep them generally recognizable for debugging, and the
			// chance we get a second-level collision (ns-a, name) and (ns, a-name) is
			// small, so we can get away with this string prefixing
			name := fmt.Sprintf("%s-%s", credential.Namespace, credential.Name)
			if s.dry {
				s.logger.AddObject(&coreapi.Secret{ObjectMeta: meta.ObjectMeta{Name: name}})
				continue
			}
			raw, err := s.secretClient.Secrets(credential.Namespace).Get(credential.Name, meta.GetOptions{})
			if err != nil {
				return fmt.Errorf("could not read source credential: %v", err)
			}
			toCreate[name] = &coreapi.Secret{
				TypeMeta: raw.TypeMeta,
				ObjectMeta: meta.ObjectMeta{
					Name:      name,
					Namespace: s.jobSpec.Namespace(),
				},
				Type:       raw.Type,
				Data:       raw.Data,
				StringData: raw.StringData,
			}
		}
	}

	for name := range toCreate {
		if _, err := s.secretClient.Secrets(s.jobSpec.Namespace()).Create(toCreate[name]); err != nil && !errors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create source credential: %v", err)
		}
	}
	return nil
}

func (s *multiStageTestStep) runSteps(
	ctx context.Context,
	steps []api.LiteralTestStep,
	env []coreapi.EnvVar,
	shortCircuit bool,
) error {
	pods, err := s.generatePods(steps, env)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.runPods(ctx, pods, shortCircuit); err != nil {
		errs = append(errs, err)
	}
	select {
	case <-ctx.Done():
		log.Printf("cleanup: Deleting pods with label %s=%s", MultiStageTestLabel, s.name)
		if !s.dry {
			if err := deletePods(s.podClient.Pods(s.jobSpec.Namespace()), s.name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete pods with label %s=%s: %v", MultiStageTestLabel, s.name, err))
			}
		}
		errs = append(errs, fmt.Errorf("cancelled"))
	default:
		break
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) generatePods(steps []api.LiteralTestStep, env []coreapi.EnvVar) ([]coreapi.Pod, error) {
	var ret []coreapi.Pod
	var errs []error
	for _, step := range steps {
		image := step.From
		if link, ok := step.FromImageTag(); ok {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, link)
		} else {
			if s.config.IsPipelineImage(image) || s.config.BuildsImage(image) {
				image = fmt.Sprintf("%s:%s", api.PipelineImageStream, image)
			} else {
				image = fmt.Sprintf("%s:%s", api.StableImageStream, image)
			}
		}
		resources, err := resourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		pod, err := generateBasePod(s.jobSpec, name, "test", []string{"/bin/bash", "-c", "#!/bin/bash\nset -eu\n" + step.Commands}, image, resources, step.ArtifactDir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		delete(pod.Labels, ProwJobIdLabel)
		pod.Annotations[annotationSaveContainerLogs] = "true"
		pod.Labels[MultiStageTestLabel] = s.name
		pod.Spec.ServiceAccountName = s.name
		addSecretWrapper(pod)
		container := &pod.Spec.Containers[0]
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "NAMESPACE", Value: s.jobSpec.Namespace()},
			{Name: "JOB_NAME_SAFE", Value: strings.Replace(s.name, "_", "-", -1)},
			{Name: "JOB_NAME_HASH", Value: s.jobSpec.JobNameHash()},
		}...)
		container.Env = append(container.Env, env...)
		container.Env = append(container.Env, s.generateParams(step.Environment)...)
		if owner := s.jobSpec.Owner(); owner != nil {
			pod.OwnerReferences = append(pod.OwnerReferences, *owner)
		}
		if s.profile != "" {
			addProfile(s.profileSecretName(), s.profile, pod)
			container.Env = append(container.Env, []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: filepath.Join(SecretMountPath, "kubeconfig")},
			}...)
		}
		addSecret(s.name, pod)
		addCredentials(step.Credentials, pod)
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
		Image:                    fmt.Sprintf("%s/ci/secret-wrapper:latest", apiCIRegistry),
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

func (s *multiStageTestStep) generateParams(env []api.StepParameter) []coreapi.EnvVar {
	var ret []coreapi.EnvVar
	for _, env := range env {
		value := env.Default
		if v, ok := s.env[env.Name]; ok {
			value = v
		}
		ret = append(ret, coreapi.EnvVar{Name: env.Name, Value: value})
	}
	return ret
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
		MountPath: SecretMountPath,
	})
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, coreapi.EnvVar{
		Name:  SecretMountEnv,
		Value: SecretMountPath,
	})
}

func addCredentials(credentials []api.CredentialReference, pod *coreapi.Pod) {
	for _, credential := range credentials {
		name := fmt.Sprintf("%s-%s", credential.Namespace, credential.Name)
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
			Name: name,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{SecretName: name},
			},
		})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      name,
			MountPath: credential.MountPath,
		})
	}
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
		MountPath: ClusterProfileMountPath,
	})
	container.Env = append(container.Env, []coreapi.EnvVar{{
		Name:  "CLUSTER_TYPE",
		Value: profile.ClusterType(),
	}, {
		Name:  ClusterProfileMountEnv,
		Value: ClusterProfileMountPath,
	}}...)
}

func (s *multiStageTestStep) runPods(ctx context.Context, pods []coreapi.Pod, shortCircuit bool) error {
	done := ctx.Done()
	namePrefix := s.name + "-"
	var errs []error
	for _, pod := range pods {
		log.Printf("Executing %q", pod.Name)
		var notifier ContainerNotifier = NopNotifier
		for _, c := range pod.Spec.Containers {
			if c.Name == "artifacts" {
				container := pod.Spec.Containers[0].Name
				dir := filepath.Join(s.artifactDir, strings.TrimPrefix(pod.Name, namePrefix))
				artifacts := NewArtifactWorker(s.podClient, dir, s.jobSpec.Namespace())
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
	if _, err := createOrRestartPod(s.podClient.Pods(s.jobSpec.Namespace()), pod); err != nil {
		return fmt.Errorf("failed to create or restart %q pod: %v", pod.Name, err)
	}
	err := waitForPodCompletion(ctx, s.podClient.Pods(s.jobSpec.Namespace()), pod.Name, notifier, false)
	s.subTests = append(s.subTests, notifier.SubTests(fmt.Sprintf("%s - %s ", s.Description(), pod.Name))...)
	if err != nil {
		return fmt.Errorf("%q pod %q failed: %v", s.name, pod.Name, err)
	}
	return nil
}

func deletePods(client coreclientset.PodInterface, test string) error {
	err := client.DeleteCollection(
		&meta.DeleteOptions{},
		meta.ListOptions{
			LabelSelector: fields.Set{
				MultiStageTestLabel: test,
			}.AsSelector().String(),
		},
	)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}
