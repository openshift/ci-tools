package steps

import (
	"context"
	"fmt"
	"log"
	"path"
	"path/filepath"
	"strings"
	"time"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/entrypoint"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	// MultiStageTestLabel is the label we use to mark a pod as part of a multi-stage test
	MultiStageTestLabel = "ci.openshift.io/multi-stage-test"
	// SkipCensoringLabel is the label we use to mark a secret as not needing to be censored
	SkipCensoringLabel = "ci.openshift.io/skip-censoring"
	// ClusterProfileMountPath is where we mount the cluster profile in a pod
	ClusterProfileMountPath = "/var/run/secrets/ci.openshift.io/cluster-profile"
	// SecretMountPath is where we mount the shared dir secret
	SecretMountPath = "/var/run/secrets/ci.openshift.io/multi-stage"
	// SecretMountEnv is the env we use to expose the shared dir
	SecretMountEnv = "SHARED_DIR"
	// ClusterProfileMountEnv is the env we use to expose the cluster profile dir
	ClusterProfileMountEnv = "CLUSTER_PROFILE_DIR"
	// CliMountPath is where we mount the cli in a pod
	CliMountPath = "/cli"
	// CliEnv if the env we use to expose the path to the cli
	CliEnv = "CLI_DIR"
	// CommandPrefix is the prefix we add to a user's commands
	CommandPrefix = "#!/bin/bash\nset -eu\n"
)

var envForProfile = []string{
	utils.ReleaseImageEnv(api.LatestReleaseName),
	utils.ImageFormatEnv,
}

type multiStageTestStep struct {
	name    string
	profile api.ClusterProfile
	config  *api.ReleaseBuildConfiguration
	// params exposes getters for variables created by other steps
	params                   api.Parameters
	env                      api.TestEnvironment
	client                   PodClient
	jobSpec                  *api.JobSpec
	pre, test, post          []api.LiteralTestStep
	subTests                 []*junit.TestCase
	subSteps                 []api.CIOperatorStepDetailInfo
	allowSkipOnSuccess       *bool
	allowBestEffortPostSteps *bool
	leases                   []api.StepLease
}

func MultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
) api.Step {
	return newMultiStageTestStep(testConfig, config, params, client, jobSpec, leases)
}

func newMultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
) *multiStageTestStep {
	ms := testConfig.MultiStageTestConfigurationLiteral
	return &multiStageTestStep{
		name:                     testConfig.As,
		profile:                  ms.ClusterProfile,
		config:                   config,
		params:                   params,
		env:                      ms.Environment,
		client:                   client,
		jobSpec:                  jobSpec,
		pre:                      ms.Pre,
		test:                     ms.Test,
		post:                     ms.Post,
		allowSkipOnSuccess:       ms.AllowSkipOnSuccess,
		allowBestEffortPostSteps: ms.AllowBestEffortPostSteps,
		leases:                   leases,
	}
}

func (s *multiStageTestStep) profileSecretName() string {
	return s.name + "-cluster-profile"
}

func (s *multiStageTestStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*multiStageTestStep) Validate() error { return nil }

func (s *multiStageTestStep) Run(ctx context.Context) error {
	return results.ForReason("executing_multi_stage_test").ForError(s.run(ctx))
}

func (s *multiStageTestStep) run(ctx context.Context) error {
	env, err := s.environment(ctx)
	if err != nil {
		return err
	}
	if err := s.createSharedDirSecret(ctx); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	if err := s.createCredentials(); err != nil {
		return fmt.Errorf("failed to create credentials: %w", err)
	}
	if err := s.setupRBAC(ctx); err != nil {
		return fmt.Errorf("failed to create RBAC objects: %w", err)
	}
	secretVolumes, secretVolumeMounts, err := secretsForCensoring(s.client, s.jobSpec.Namespace(), ctx)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.runSteps(ctx, s.pre, env, true, false, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %w", s.name, err))
	} else if err := s.runSteps(ctx, s.test, env, true, len(errs) != 0, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %w", s.name, err))
	}
	if err := s.runSteps(context.Background(), s.post, env, false, len(errs) != 0, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q post steps failed: %w", s.name, err))
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) Name() string { return s.name }
func (s *multiStageTestStep) Description() string {
	return fmt.Sprintf("Run multi-stage test %s", s.name)
}
func (s *multiStageTestStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *multiStageTestStep) SubSteps() []api.CIOperatorStepDetailInfo {
	return s.subSteps
}

func (s *multiStageTestStep) Requires() (ret []api.StepLink) {
	var needsReleaseImage, needsReleasePayload bool
	for _, step := range append(append(s.pre, s.test...), s.post...) {
		if link, ok := step.FromImageTag(); ok {
			ret = append(ret, api.InternalImageLink(link))
		} else {
			dependency := api.StepDependency{Name: step.From}
			imageStream, name, explicit := s.config.DependencyParts(dependency)
			if explicit {
				ret = append(ret, api.LinkForImage(imageStream, name))
			} else {
				// if the user did not specify an explicit namespace for this image,
				// it's likely coming from an imported release we need to wait for
				needsReleaseImage = true
			}
		}

		for _, dependency := range step.Dependencies {
			// we validate that the link will exist at config load time
			// so we can safely ignore the case where !ok
			imageStream, name, _ := s.config.DependencyParts(dependency)
			ret = append(ret, api.LinkForImage(imageStream, name))
		}

		if step.Cli != "" {
			dependency := api.StepDependency{Name: fmt.Sprintf("%s:cli", api.ReleaseStreamFor(step.Cli))}
			imageStream, name, _ := s.config.DependencyParts(dependency)
			ret = append(ret, api.LinkForImage(imageStream, name))
		}
	}
	if s.profile != "" {
		needsReleasePayload = true
		for _, env := range envForProfile {
			if link, ok := utils.LinkForEnv(env); ok {
				ret = append(ret, link)
			}
		}
	}
	if needsReleaseImage && !needsReleasePayload {
		ret = append(ret, api.ReleaseImagesLink(api.LatestReleaseName))
	}
	return
}

func (s *multiStageTestStep) Creates() []api.StepLink { return nil }
func (s *multiStageTestStep) Provides() api.ParameterMap {
	return nil
}
func (s *multiStageTestStep) SubTests() []*junit.TestCase { return s.subTests }

func (s *multiStageTestStep) setupRBAC(ctx context.Context) error {
	labels := map[string]string{MultiStageTestLabel: s.name}
	m := meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: s.name, Labels: labels}
	sa := &coreapi.ServiceAccount{ObjectMeta: m}
	role := &rbacapi.Role{
		ObjectMeta: m,
		Rules: []rbacapi.PolicyRule{{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings", "roles"},
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
	bindings := []rbacapi.RoleBinding{
		{
			ObjectMeta: m,
			RoleRef:    rbacapi.RoleRef{Kind: "Role", Name: s.name},
			Subjects:   subj,
		},
		{
			ObjectMeta: meta.ObjectMeta{Namespace: s.jobSpec.Namespace(), Name: "test-runner-view-binding", Labels: labels},
			RoleRef:    rbacapi.RoleRef{Kind: "ClusterRole", Name: "view"},
			Subjects:   subj,
		},
	}

	if err := util.CreateRBACs(ctx, sa, role, bindings, s.client, 1*time.Second, 1*time.Minute); err != nil {
		return err
	}

	return nil
}

func (s *multiStageTestStep) environment(ctx context.Context) ([]coreapi.EnvVar, error) {
	var ret []coreapi.EnvVar
	for _, l := range s.leases {
		val, err := s.params.Get(l.Env)
		if err != nil {
			return nil, err
		}
		ret = append(ret, coreapi.EnvVar{Name: l.Env, Value: val})
	}
	if s.profile != "" {
		secret := s.profileSecretName()
		if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: secret}, &coreapi.Secret{}); err != nil {
			return nil, fmt.Errorf("could not find secret %q: %w", secret, err)
		}
		for _, e := range envForProfile {
			val, err := s.params.Get(e)
			if err != nil {
				return nil, err
			}
			ret = append(ret, coreapi.EnvVar{Name: e, Value: val})
		}
		if optionalOperator, err := resolveOptionalOperator(s.params); err != nil {
			return nil, err
		} else if optionalOperator != nil {
			ret = append(ret, optionalOperator.asEnv()...)
		}
	}
	return ret, nil
}

func (s *multiStageTestStep) createSharedDirSecret(ctx context.Context) error {
	log.Printf("Creating multi-stage test shared directory %q", s.name)
	secret := &coreapi.Secret{ObjectMeta: meta.ObjectMeta{
		Namespace: s.jobSpec.Namespace(),
		Name:      s.name,
		Labels:    map[string]string{SkipCensoringLabel: "true"},
	}}
	if err := s.client.Delete(ctx, secret); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("cannot delete shared directory %q: %w", s.name, err)
	}
	return s.client.Create(ctx, secret)
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
			raw := &coreapi.Secret{}
			if err := s.client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: credential.Namespace, Name: credential.Name}, raw); err != nil {
				return fmt.Errorf("could not read source credential: %w", err)
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
		if err := s.client.Create(context.TODO(), toCreate[name]); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create source credential: %w", err)
		}
	}
	return nil
}

func (s *multiStageTestStep) runSteps(
	ctx context.Context,
	steps []api.LiteralTestStep,
	env []coreapi.EnvVar,
	shortCircuit bool,
	hasPrevErrs bool,
	secretVolumes []coreapi.Volume,
	secretVolumeMounts []coreapi.VolumeMount,
) error {
	pods, isBestEffort, err := s.generatePods(steps, env, hasPrevErrs, secretVolumes, secretVolumeMounts)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.runPods(ctx, pods, shortCircuit, isBestEffort); err != nil {
		errs = append(errs, err)
	}
	select {
	case <-ctx.Done():
		log.Printf("cleanup: Deleting pods with label %s=%s", MultiStageTestLabel, s.name)

		// Simplify to DeleteAllOf when https://bugzilla.redhat.com/show_bug.cgi?id=1937523 is fixed across production.
		podList := &coreapi.PodList{}
		if err := s.client.List(cleanupCtx, podList, ctrlruntimeclient.InNamespace(s.jobSpec.Namespace()), ctrlruntimeclient.MatchingLabels{MultiStageTestLabel: s.name}); err != nil {
			errs = append(errs, fmt.Errorf("failed to list pods with label %s=%s: %w", MultiStageTestLabel, s.name, err))
		} else {
			for _, pod := range podList.Items {
				if pod.Status.Phase == coreapi.PodSucceeded || pod.Status.Phase == coreapi.PodFailed || pod.DeletionTimestamp != nil {
					// Ignore pods that are complete or on their way out.
					continue
				}
				if err := s.client.Delete(cleanupCtx, &pod); err != nil && !kerrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to delete pod %s with label %s=%s: %w", pod.Name, MultiStageTestLabel, s.name, err))
				}
			}
		}
		errs = append(errs, fmt.Errorf("cancelled"))
	default:
		break
	}
	return utilerrors.NewAggregate(errs)
}

const multiStageTestStepContainerName = "test"

func (s *multiStageTestStep) generatePods(steps []api.LiteralTestStep, env []coreapi.EnvVar,
	hasPrevErrs bool, secretVolumes []coreapi.Volume, secretVolumeMounts []coreapi.VolumeMount) ([]coreapi.Pod, func(string) bool, error) {
	bestEffort := sets.NewString()
	isBestEffort := func(podName string) bool {
		if s.allowBestEffortPostSteps == nil || !*s.allowBestEffortPostSteps {
			// the user has not requested best-effort steps or they've explicitly disabled them
			return false
		}
		return bestEffort.Has(podName)
	}
	var ret []coreapi.Pod
	var errs []error
	for _, step := range steps {
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		if s.allowSkipOnSuccess != nil && *s.allowSkipOnSuccess &&
			step.OptionalOnSuccess != nil && *step.OptionalOnSuccess &&
			!hasPrevErrs {
			log.Println(fmt.Sprintf("Skipping optional step %q", name))
			continue
		}
		image := step.From
		if link, ok := step.FromImageTag(); ok {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, link)
		} else {
			dep := api.StepDependency{Name: image}
			stream, tag, _ := s.config.DependencyParts(dep)
			image = fmt.Sprintf("%s:%s", stream, tag)
		}
		resources, err := resourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if step.BestEffort != nil && *step.BestEffort {
			bestEffort.Insert(name)
		}
		p := func(i int64) *int64 {
			return &i
		}
		artifactDir := fmt.Sprintf("%s/%s", s.name, step.As)
		timeout := entrypoint.DefaultTimeout
		if step.Timeout != nil {
			timeout = step.Timeout.Duration
		}
		s.jobSpec.DecorationConfig.Timeout = &prowapi.Duration{Duration: timeout}
		gracePeriod := entrypoint.DefaultGracePeriod
		if step.GracePeriod != nil {
			gracePeriod = step.GracePeriod.Duration
		}
		s.jobSpec.DecorationConfig.GracePeriod = &prowapi.Duration{Duration: gracePeriod}
		// We want upload to have some time to do what it needs to do, so set
		// the grace period for the Pod to be just larger than the grace period
		// for the process, assuming an 80/20 distribution of work.
		terminationGracePeriodSeconds := p(int64(gracePeriod.Seconds() * 5 / 4))
		pod, err := generateBasePod(s.jobSpec, name, multiStageTestStepContainerName, []string{"/bin/bash", "-c", CommandPrefix + step.Commands}, image, resources, artifactDir, s.jobSpec.DecorationConfig, s.jobSpec.RawSpec(), secretVolumeMounts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		delete(pod.Labels, ProwJobIdLabel)
		pod.Annotations[annotationSaveContainerLogs] = "true"
		pod.Labels[MultiStageTestLabel] = s.name
		pod.Spec.ServiceAccountName = s.name
		pod.Spec.TerminationGracePeriodSeconds = terminationGracePeriodSeconds
		if step.DNSConfig != nil {
			if pod.Spec.DNSConfig == nil {
				pod.Spec.DNSConfig = &coreapi.PodDNSConfig{}
			}
			pod.Spec.DNSConfig.Searches = append(pod.Spec.DNSConfig.Searches, step.DNSConfig.Searches...)
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{Name: homeVolumeName, VolumeSource: coreapi.VolumeSource{EmptyDir: &coreapi.EmptyDirVolumeSource{}}})
		pod.Spec.Volumes = append(pod.Spec.Volumes, secretVolumes...)
		for idx := range pod.Spec.Containers {
			if pod.Spec.Containers[idx].Name != multiStageTestStepContainerName {
				continue
			}
			pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, coreapi.VolumeMount{Name: homeVolumeName, MountPath: "/alabama"})
		}

		addSecretWrapper(pod)
		container := &pod.Spec.Containers[0]
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "NAMESPACE", Value: s.jobSpec.Namespace()},
			{Name: "JOB_NAME_SAFE", Value: strings.Replace(s.name, "_", "-", -1)},
			{Name: "JOB_NAME_HASH", Value: s.jobSpec.JobNameHash()},
		}...)
		container.Env = append(container.Env, env...)
		container.Env = append(container.Env, s.generateParams(step.Environment)...)
		depEnv, depErrs := s.envForDependencies(step)
		if len(depErrs) != 0 {
			errs = append(errs, depErrs...)
			continue
		}
		container.Env = append(container.Env, depEnv...)
		if owner := s.jobSpec.Owner(); owner != nil {
			pod.OwnerReferences = append(pod.OwnerReferences, *owner)
		}
		if s.profile != "" {
			addProfile(s.profileSecretName(), s.profile, pod)
			container.Env = append(container.Env, []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: filepath.Join(SecretMountPath, "kubeconfig")},
				{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(SecretMountPath, "kubeadmin-password")},
			}...)
		}
		if step.Cli != "" {
			addCliInjector(step.Cli, pod)
		}
		addSharedDirSecret(s.name, pod)
		addCredentials(step.Credentials, pod)
		ret = append(ret, *pod)
	}
	return ret, isBestEffort, utilerrors.NewAggregate(errs)
}

// secretsForCensoring returns the secret volumes and mounts that will allow sidecar to censor
// their content from uploads. This is the full secret list in our namespace, except for the ones
// we created to store shared directory content.
func secretsForCensoring(client PodClient, namespace string, ctx context.Context) ([]coreapi.Volume, []coreapi.VolumeMount, error) {
	secretList := coreapi.SecretList{}
	if err := client.List(ctx, &secretList, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return nil, nil, fmt.Errorf("could not list secrets to determine content to censor: %w", err)
	}
	var secretVolumes []coreapi.Volume
	var secretVolumeMounts []coreapi.VolumeMount
	for _, secret := range secretList.Items {
		if _, skip := secret.ObjectMeta.Labels[SkipCensoringLabel]; skip {
			continue
		}
		volumeName := fmt.Sprintf("censor-%s", secret.Name)
		secretVolumes = append(secretVolumes, coreapi.Volume{
			Name: volumeName,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: secret.Name,
				},
			},
		})
		secretVolumeMounts = append(secretVolumeMounts, coreapi.VolumeMount{
			Name:      volumeName,
			MountPath: path.Join("/secrets", secret.Name),
		})
	}
	return secretVolumes, secretVolumeMounts, nil
}

func (s *multiStageTestStep) envForDependencies(step api.LiteralTestStep) ([]coreapi.EnvVar, []error) {
	var env []coreapi.EnvVar
	var errs []error
	for _, dependency := range step.Dependencies {
		imageStream, name, _ := s.config.DependencyParts(dependency)
		ref, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, imageStream, name)()
		if err != nil {
			errs = append(errs, fmt.Errorf("could not determine image pull spec for image %s on step %s", dependency.Name, step.As))
			continue
		}
		env = append(env, coreapi.EnvVar{
			Name: dependency.Env, Value: ref,
		})
	}
	return env, errs
}

func addSecretWrapper(pod *coreapi.Pod) {
	volume := "entrypoint-wrapper"
	dir := "/tmp/entrypoint-wrapper"
	bin := filepath.Join(dir, "entrypoint-wrapper")
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volume,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	mount := coreapi.VolumeMount{Name: volume, MountPath: dir}
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Image:                    fmt.Sprintf("%s/ci/entrypoint-wrapper:latest", ciRegistry),
		Name:                     "cp-entrypoint-wrapper",
		Command:                  []string{"cp"},
		Args:                     []string{"/bin/entrypoint-wrapper", bin},
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
		value := ""
		if env.Default != nil {
			value = *env.Default
		}
		if v, ok := s.env[env.Name]; ok {
			value = v
		}
		ret = append(ret, coreapi.EnvVar{Name: env.Name, Value: value})
	}
	return ret
}

func addSharedDirSecret(secret string, pod *coreapi.Pod) {
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

func addCliInjector(release string, pod *coreapi.Pod) {
	volumeName := "cli"
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Name:    "inject-cli",
		Image:   fmt.Sprintf("%s:cli", api.ReleaseStreamFor(release)),
		Command: []string{"/bin/cp"},
		Args:    []string{"/usr/bin/oc", CliMountPath},
		VolumeMounts: []coreapi.VolumeMount{{
			Name:      volumeName,
			MountPath: CliMountPath,
		}},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      volumeName,
		MountPath: CliMountPath,
	})
	container.Env = append(container.Env, coreapi.EnvVar{
		Name:  CliEnv,
		Value: CliMountPath,
	})
}

func (s *multiStageTestStep) runPods(ctx context.Context, pods []coreapi.Pod, shortCircuit bool, isBestEffort func(string) bool) error {
	var errs []error
	for _, pod := range pods {
		err := s.runPod(ctx, &pod, NewTestCaseNotifier(NopNotifier))
		if err != nil {
			if isBestEffort(pod.Name) {
				log.Println(fmt.Sprintf("Pod %s is running in best-effort mode, ignoring the failure...", pod.Name))
				continue
			}
			errs = append(errs, err)
			if shortCircuit {
				break
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (s *multiStageTestStep) runPod(ctx context.Context, pod *coreapi.Pod, notifier *TestCaseNotifier) error {
	start := time.Now()
	client := s.client.WithNewLoggingClient()
	if _, err := createOrRestartPod(client, pod); err != nil {
		return fmt.Errorf("failed to create or restart %q pod: %w", pod.Name, err)
	}
	newPod, err := waitForPodCompletion(ctx, client, pod.Namespace, pod.Name, notifier, false)
	if newPod != nil {
		pod = newPod
	}
	finished := time.Now()
	duration := finished.Sub(start)
	s.subSteps = append(s.subSteps, api.CIOperatorStepDetailInfo{
		StepName:    pod.Name,
		Description: fmt.Sprintf("Run pod %s", pod.Name),
		StartedAt:   &start,
		FinishedAt:  &finished,
		Duration:    &duration,
		Failed:      utilpointer.BoolPtr(err != nil),
		Manifests:   client.Objects(),
	})
	s.subTests = append(s.subTests, notifier.SubTests(fmt.Sprintf("%s - %s ", s.Description(), pod.Name))...)
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
