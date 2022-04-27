package multi_stage

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// stepFlag controls the behavior of a test throughout its execution.
type stepFlag uint8

const (
	// A test failure should terminate the current phase.
	// Set for `pre` and `test`, unset for `post`.
	shortCircuit = stepFlag(1) << iota
	// There was a failure in any of the previous steps.
	// Used in the implementation of best-effort steps.
	hasPrevErrs
	// The test was configured to allow "skip on success" steps.
	allowSkipOnSuccess
	// The test was configured to allow best-effort steps.
	allowBestEffortPostSteps
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
	// CliMountPath is where we mount the cli in a pod
	CliMountPath = "/cli"
	// CommandPrefix is the prefix we add to a user's commands
	CommandPrefix = "#!/bin/bash\nset -eu\n"
	// CommandScriptMountPath is where we mount the command script
	CommandScriptMountPath = "/var/run/configmaps/ci.openshift.io/multi-stage"
	homeVolumeName         = "home"
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
	params          api.Parameters
	env             api.TestEnvironment
	client          kubernetes.PodClient
	jobSpec         *api.JobSpec
	pre, test, post []api.LiteralTestStep
	subTests        []*junit.TestCase
	subSteps        []api.CIOperatorStepDetailInfo
	flags           stepFlag
	leases          []api.StepLease
	clusterClaim    *api.ClusterClaim
}

func MultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client kubernetes.PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
) api.Step {
	return newMultiStageTestStep(testConfig, config, params, client, jobSpec, leases)
}

func newMultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client kubernetes.PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
) *multiStageTestStep {
	ms := testConfig.MultiStageTestConfigurationLiteral
	var flags stepFlag
	if p := ms.AllowSkipOnSuccess; p != nil && *p {
		flags |= allowSkipOnSuccess
	}
	if p := ms.AllowBestEffortPostSteps; p != nil && *p {
		flags |= allowBestEffortPostSteps
	}
	return &multiStageTestStep{
		name:         testConfig.As,
		profile:      ms.ClusterProfile,
		config:       config,
		params:       params,
		env:          ms.Environment,
		client:       client,
		jobSpec:      jobSpec,
		pre:          ms.Pre,
		test:         ms.Test,
		post:         ms.Post,
		flags:        flags,
		leases:       leases,
		clusterClaim: testConfig.ClusterClaim,
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
	if s.profile != "" {
		s.getProfileData(ctx)
	}
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
	s.flags |= shortCircuit
	if err := s.runSteps(ctx, "pre", s.pre, env, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %w", s.name, err))
	} else if err := s.runSteps(ctx, "test", s.test, env, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %w", s.name, err))
	}
	s.flags &= ^shortCircuit
	if err := s.runSteps(context.Background(), "post", s.post, env, secretVolumes, secretVolumeMounts); err != nil {
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

func (s *multiStageTestStep) getProfileData(ctx context.Context) error {
	var secret coreapi.Secret
	name := s.profileSecretName()
	key := ctrlruntimeclient.ObjectKey{
		Namespace: s.jobSpec.Namespace(),
		Name:      name,
	}
	if err := s.client.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("could not get cluster profile secret %q: %w", name, err)
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
		if _, skip := secret.ObjectMeta.Labels[api.SkipCensoringLabel]; skip {
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

func volumeName(ns, name string) string {
	return strings.ReplaceAll(fmt.Sprintf("%s-%s", ns, name), ".", "-")
}
