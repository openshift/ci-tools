package steps

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/entrypoint"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
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
	// CommandScriptMountPath is where we mount the command script
	CommandScriptMountPath = "/var/run/configmaps/ci.openshift.io/multi-stage"
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
	clusterClaim             *api.ClusterClaim
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
		clusterClaim:             testConfig.ClusterClaim,
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
	logrus.Infof("Running multi-stage test %s", s.name)
	env, err := s.environment(ctx)
	if err != nil {
		return err
	}
	if err := s.createSharedDirSecret(ctx); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	if err := s.createCredentials(ctx); err != nil {
		return fmt.Errorf("failed to create credentials: %w", err)
	}
	if err := s.createCommandConfigMaps(ctx); err != nil {
		return fmt.Errorf("failed to create command configmap: %w", err)
	}
	if err := s.setupRBAC(ctx); err != nil {
		return fmt.Errorf("failed to create RBAC objects: %w", err)
	}
	secretVolumes, secretVolumeMounts, err := secretsForCensoring(s.client, s.jobSpec.Namespace(), ctx)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.runSteps(ctx, "pre", s.pre, env, true, false, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %w", s.name, err))
	} else if err := s.runSteps(ctx, "test", s.test, env, true, len(errs) != 0, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %w", s.name, err))
	}
	if err := s.runSteps(context.Background(), "post", s.post, env, false, len(errs) != 0, secretVolumes, secretVolumeMounts); err != nil {
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
	var claimRelease *api.ClaimRelease
	if s.clusterClaim != nil {
		claimRelease = s.clusterClaim.ClaimRelease(s.name)
	}
	var needsReleaseImage, needsReleasePayload bool
	for _, step := range append(append(s.pre, s.test...), s.post...) {
		if link, ok := step.FromImageTag(); ok {
			ret = append(ret, api.InternalImageLink(link))
		} else {
			dependency := api.StepDependency{Name: step.From}
			imageStream, name, explicit := s.config.DependencyParts(dependency, claimRelease)
			if explicit {
				ret = append(ret, api.LinkForImage(imageStream, name))
			} else {
				// if the user did not specify an explicit namespace for this image,
				// it's likely coming from an imported release we need to wait for
				needsReleaseImage = true
			}
		}

		for _, dependency := range step.Dependencies {
			// if a fully-qualified pull spec was provided to the ci-operator for this dependency, then we don't need to
			// create a step link since we won't do anything with this dependency other than passing the pull spec straight
			// through in the environment variable.
			if dependency.PullSpec != "" {
				continue
			}

			// we validate that the link will exist at config load time
			// so we can safely ignore the case where !ok
			imageStream, name, _ := s.config.DependencyParts(dependency, claimRelease)
			ret = append(ret, api.LinkForImage(imageStream, name))
		}

		if step.Cli != "" {
			dependency := api.StepDependency{Name: fmt.Sprintf("%s:cli", api.ReleaseStreamFor(step.Cli))}
			imageStream, name, _ := s.config.DependencyParts(dependency, claimRelease)
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
		releaseName := api.LatestReleaseName
		if claimRelease != nil && claimRelease.OverrideName == api.LatestReleaseName {
			releaseName = claimRelease.ReleaseName
		}
		ret = append(ret, api.ReleaseImagesLink(releaseName))
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
	}
	return ret, nil
}

func (s *multiStageTestStep) createSharedDirSecret(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test shared directory %q", s.name)
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

func (s *multiStageTestStep) createCredentials(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test credentials for %q", s.name)
	toCreate := map[string]*coreapi.Secret{}
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		for _, credential := range step.Credentials {
			// we don't want secrets imported from separate namespaces to collide
			// but we want to keep them generally recognizable for debugging, and the
			// chance we get a second-level collision (ns-a, name) and (ns, a-name) is
			// small, so we can get away with this string prefixing
			name := fmt.Sprintf("%s-%s", credential.Namespace, credential.Name)
			if _, ok := toCreate[name]; ok {
				continue
			}
			raw := &coreapi.Secret{}
			if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: credential.Namespace, Name: credential.Name}, raw); err != nil {
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
		if err := s.client.Create(ctx, toCreate[name]); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create source credential: %w", err)
		}
	}
	return nil
}

func (s *multiStageTestStep) createCommandConfigMaps(ctx context.Context) error {
	logrus.Debugf("Creating multi-stage test commands configmap for %q", s.name)
	data := make(map[string]string)
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		data[step.As] = step.Commands
	}
	name := commandConfigMapForTest(s.name)
	yes := true
	commands := &coreapi.ConfigMap{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: s.jobSpec.Namespace(),
		},
		Data:      data,
		Immutable: &yes,
	}
	// delete old command configmap if it exists
	if err := s.client.Delete(ctx, commands); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("could not delete command configmap %s: %w", name, err)
	}
	if err := s.client.Create(ctx, commands); err != nil {
		return fmt.Errorf("could not create command configmap %s: %w", name, err)
	}
	return nil
}

func commandConfigMapForTest(testName string) string {
	return fmt.Sprintf("%s-commands", testName)
}

func (s *multiStageTestStep) runSteps(
	ctx context.Context,
	phase string,
	steps []api.LiteralTestStep,
	env []coreapi.EnvVar,
	shortCircuit bool,
	hasPrevErrs bool,
	secretVolumes []coreapi.Volume,
	secretVolumeMounts []coreapi.VolumeMount,
) error {
	start := time.Now()
	logrus.Infof("Running multi-stage phase %s", phase)
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
		logrus.Infof("cleanup: Deleting pods with label %s=%s", MultiStageTestLabel, s.name)

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
					continue
				}
				if err := waitForPodDeletion(cleanupCtx, s.client, s.jobSpec.Namespace(), pod.Name, pod.UID); err != nil {
					errs = append(errs, fmt.Errorf("failed waiting for pod %s with label %s=%s to be deleted: %w", pod.Name, MultiStageTestLabel, s.name, err))
					continue
				}
			}
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
	var claimRelease *api.ClaimRelease
	if s.clusterClaim != nil {
		claimRelease = s.clusterClaim.ClaimRelease(s.name)
	}
	for _, step := range steps {
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		if s.allowSkipOnSuccess != nil && *s.allowSkipOnSuccess &&
			step.OptionalOnSuccess != nil && *step.OptionalOnSuccess &&
			!hasPrevErrs {
			logrus.Infof(fmt.Sprintf("Skipping optional step %s", name))
			continue
		}
		image := step.From
		if link, ok := step.FromImageTag(); ok {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, link)
		} else {
			dep := api.StepDependency{Name: image}
			stream, tag, _ := s.config.DependencyParts(dep, claimRelease)
			image = fmt.Sprintf("%s:%s", stream, tag)
		}
		resources, err := resourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allResources := &resources
		if !resources.Requests.Name(api.ShmResource, resource.BinarySI).IsZero() {
			// If shm is in Limits it must also be in Requests
			allResources = resources.DeepCopy()
			logrus.Info("removing shm from resources for container")
			delete(resources.Requests, api.ShmResource)
			delete(resources.Limits, api.ShmResource)
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
		var commands []string
		if step.RunAsScript != nil && *step.RunAsScript {
			commands = []string{fmt.Sprintf("%s/%s", CommandScriptMountPath, step.As)}
		} else {
			commands = []string{"/bin/bash", "-c", CommandPrefix + step.Commands}
		}
		labels := map[string]string{LabelMetadataStep: step.As}
		pod, err := generateBasePod(s.jobSpec, labels, name, multiStageTestStepContainerName, commands, image, resources, artifactDir, s.jobSpec.DecorationConfig, s.jobSpec.RawSpec(), secretVolumeMounts, false)
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
			pod.Spec.DNSConfig.Nameservers = append(pod.Spec.DNSConfig.Nameservers, step.DNSConfig.Nameservers...)
			pod.Spec.DNSConfig.Searches = append(pod.Spec.DNSConfig.Searches, step.DNSConfig.Searches...)
			if len(pod.Spec.DNSConfig.Nameservers) > 0 {
				pod.Spec.DNSPolicy = coreapi.DNSNone
			}
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
		if s.profile != "" && s.clusterClaim != nil {
			// should never happen
			errs = append(errs, fmt.Errorf("cannot set both cluster_profile and cluster_claim in a test"))
		}
		if s.clusterClaim != nil {
			clusterClaimEnv, clusterClaimMount, err := getClusterClaimPodParams(secretVolumeMounts, s.name)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get cluster claim pod params: %w", err))
			} else {
				container.Env = append(container.Env, clusterClaimEnv...)
				// The volumes are there already because sidecar container uses them.
				// We mount them here to the test container.
				container.VolumeMounts = append(container.VolumeMounts, clusterClaimMount...)
			}
		} else {
			container.Env = append(container.Env, []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: filepath.Join(SecretMountPath, "kubeconfig")},
				{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(SecretMountPath, "kubeadmin-password")},
			}...)
		}
		shmSize := allResources.Requests.Name(api.ShmResource, resource.BinarySI)
		if !shmSize.IsZero() {
			addDshmVolume(shmSize, pod, container)
		}
		if s.profile != "" {
			addProfile(s.profileSecretName(), s.profile, pod)
		}
		if step.Cli != "" {
			dependency := api.StepDependency{Name: fmt.Sprintf("%s:cli", api.ReleaseStreamFor(step.Cli))}
			imagestream, _, _ := s.config.DependencyParts(dependency, claimRelease)
			addCliInjector(imagestream, pod)
		}
		addSharedDirSecret(s.name, pod)
		addCredentials(step.Credentials, pod)
		if step.RunAsScript != nil && *step.RunAsScript {
			addCommandScript(commandConfigMapForTest(s.name), pod)
		}
		ret = append(ret, *pod)
	}
	return ret, isBestEffort, utilerrors.NewAggregate(errs)
}

// secretsForCensoring returns the secret volumes and mounts that will allow sidecar to censor
// their content from uploads. This is the full secret list in our namespace, except for the ones
// we created to store shared directory content and autogenerated secrets for ServiceAccounts.
func secretsForCensoring(client loggingclient.LoggingClient, namespace string, ctx context.Context) ([]coreapi.Volume, []coreapi.VolumeMount, error) {
	secretList := coreapi.SecretList{}
	if err := client.List(ctx, &secretList, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return nil, nil, fmt.Errorf("could not list secrets to determine content to censor: %w", err)
	}
	var secretVolumes []coreapi.Volume
	var secretVolumeMounts []coreapi.VolumeMount
	for i, secret := range secretList.Items {
		if _, skip := secret.ObjectMeta.Labels[SkipCensoringLabel]; skip {
			continue
		}
		if _, skip := secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]; skip {
			continue
		}
		volumeName := fmt.Sprintf("censor-%d", i)
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
			MountPath: getMountPath(secret.Name),
		})
	}
	return secretVolumes, secretVolumeMounts, nil
}

func getMountPath(secretName string) string {
	return path.Join("/secrets", secretName)
}

func (s *multiStageTestStep) envForDependencies(step api.LiteralTestStep) ([]coreapi.EnvVar, []error) {
	var env []coreapi.EnvVar
	var errs []error
	var claimRelease *api.ClaimRelease
	if s.clusterClaim != nil {
		claimRelease = s.clusterClaim.ClaimRelease(s.name)
	}
	for _, dependency := range step.Dependencies {
		var ref string
		// if a fully-qualified pull spec was provided, then just use that. It'll be up to the job to use that pull spec
		// correctly as it could possibly point to an external registry that ci-operator will itself not have access to.
		if dependency.PullSpec != "" {
			ref = dependency.PullSpec
		} else {
			imageStream, name, _ := s.config.DependencyParts(dependency, claimRelease)
			depRef, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, imageStream, name)()
			if err != nil {
				errs = append(errs, fmt.Errorf("could not determine image pull spec for image %s on step %s", dependency.Name, step.As))
				continue
			}
			ref = depRef
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
		volumeName := volumeName(credential.Namespace, credential.Name)
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
			Name: volumeName,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{SecretName: name},
			},
		})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      volumeName,
			MountPath: credential.MountPath,
		})
	}
}

func addDshmVolume(shmSize *resource.Quantity, pod *coreapi.Pod, container *coreapi.Container) {
	logrus.Infof("Adding Dshm Volume to pod: %s", pod.Name)
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: "dshm",
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{
				Medium:    coreapi.StorageMediumMemory,
				SizeLimit: shmSize}},
	})
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      "dshm",
		MountPath: "/dev/shm",
	})
}

func volumeName(ns, name string) string {
	return strings.ReplaceAll(fmt.Sprintf("%s-%s", ns, name), ".", "-")
}

// ValidateSecretInStep validates a secret used in a step
func ValidateSecretInStep(ns, name string) error {
	// only secrets in test-credentials namespace can be used in a step
	if ns != "test-credentials" {
		return nil
	}
	volumeName := volumeName(ns, name)
	if valueErrs := validation.IsDNS1123Label(volumeName); len(valueErrs) > 0 {
		return fmt.Errorf("volumeName %s: %v", volumeName, valueErrs)
	}
	return nil
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

func addCommandScript(name string, pod *coreapi.Pod) {
	volumeName := "commands-script"
	// 0777 in decimal is 511
	mode := int32(511)
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			ConfigMap: &coreapi.ConfigMapVolumeSource{
				LocalObjectReference: coreapi.LocalObjectReference{
					Name: name,
				},
				DefaultMode: &mode,
			},
		},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      volumeName,
		MountPath: CommandScriptMountPath,
	})
}

func addCliInjector(imagestream string, pod *coreapi.Pod) {
	volumeName := "cli"
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Name:    "inject-cli",
		Image:   fmt.Sprintf("%s:cli", imagestream),
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
				logrus.Infof("Pod %s is running in best-effort mode, ignoring the failure...", pod.Name)
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
	logrus.Infof("Running step %s.", pod.Name)
	client := s.client.WithNewLoggingClient()
	if _, err := createOrRestartPod(ctx, client, pod); err != nil {
		return fmt.Errorf("failed to create or restart %s pod: %w", pod.Name, err)
	}
	newPod, err := waitForPodCompletion(ctx, client, pod.Namespace, pod.Name, notifier, false)
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

func getClusterClaimPodParams(secretVolumeMounts []coreapi.VolumeMount, testName string) ([]coreapi.EnvVar, []coreapi.VolumeMount, error) {
	var retEnv []coreapi.EnvVar
	var retMount []coreapi.VolumeMount
	var errs []error

	for _, secretName := range []string{api.HiveAdminKubeconfigSecret, api.HiveAdminPasswordSecret} {
		mountPath := getMountPath(namePerTest(secretName, testName))
		var foundMountPath bool
		for _, secretVolumeMount := range secretVolumeMounts {
			if secretVolumeMount.MountPath == mountPath {
				foundMountPath = true
				retMount = append(retMount, secretVolumeMount)
				if secretName == api.HiveAdminKubeconfigSecret {
					retEnv = append(retEnv, coreapi.EnvVar{Name: "KUBECONFIG", Value: filepath.Join(secretVolumeMount.MountPath, api.HiveAdminKubeconfigSecretKey)})
				}
				if secretName == api.HiveAdminPasswordSecret {
					retEnv = append(retEnv, coreapi.EnvVar{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(secretVolumeMount.MountPath, api.HiveAdminPasswordSecretKey)})
				}
				break
			}
		}
		if !foundMountPath {
			// should never happen
			errs = append(errs, fmt.Errorf("failed to find foundMountPath %s to create secret %s", mountPath, namePerTest(secretName, testName)))
		}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}
	return retEnv, retMount, nil
}
