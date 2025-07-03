package multi_stage

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// stepFlag controls the behavior of a test throughout its execution.
type stepFlag uint8

// vpnConf is the format of the VPN configuration file in the cluster profile.
// The presence of this file triggers the addition of a VPN client to each step
// pod according to information in this configuration.
type vpnConf struct {
	// image is the pull spec of the image used for the container.
	Image string `json:"image"`
	// commands is the entry point of the container, executed as a bash script.
	// Initially refers to the key in the Secret, later replaced by the actual
	// script.
	Commands string `json:"commands"`
	// waitTimeout is how long to wait for the connection before failing.
	WaitTimeout *string `json:"wait_timeout"`
	// Runtime data for the step, not present in the configuration.
	namespaceUID int64
}

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
	// vpnConfPath is the path of the configuration file in the cluster profile.
	vpnConfPath = "vpn.yaml"
)

var envForProfile = []string{
	utils.ReleaseImageEnv(api.LatestReleaseName),
	utils.ImageFormatEnv,
}

type multiStageTestStep struct {
	name             string
	additionalSuffix string
	nodeName         string
	profile          api.ClusterProfile
	config           *api.ReleaseBuildConfiguration
	// params exposes getters for variables created by other steps
	params                      api.Parameters
	env                         api.TestEnvironment
	client                      kubernetes.PodClient
	jobSpec                     *api.JobSpec
	observers                   []api.Observer
	pre, test, post             []api.LiteralTestStep
	subLock                     *sync.Mutex
	subTests                    []*junit.TestCase
	subSteps                    []api.CIOperatorStepDetailInfo
	flags                       stepFlag
	leases                      []api.StepLease
	clusterClaim                *api.ClusterClaim
	vpnConf                     *vpnConf
	cancelObservers             func(context.CancelFunc)
	nodeArchitecture            api.NodeArchitecture
	enableSecretsStoreCSIDriver bool
}

func MultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client kubernetes.PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
	nodeName string,
	targetAdditionalSuffix string,
	cancelObservers func(context.CancelFunc),
	enableSecretsStoreCSIDriver bool,
) api.Step {
	return newMultiStageTestStep(testConfig, config, params, client, jobSpec, leases, nodeName, targetAdditionalSuffix, cancelObservers, enableSecretsStoreCSIDriver)
}

func newMultiStageTestStep(
	testConfig api.TestStepConfiguration,
	config *api.ReleaseBuildConfiguration,
	params api.Parameters,
	client kubernetes.PodClient,
	jobSpec *api.JobSpec,
	leases []api.StepLease,
	nodeName string,
	targetAdditionalSuffix string,
	cancelObservers func(context.CancelFunc),
	enableSecretsStoreCSIDriver bool,
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
		name:                        testConfig.As,
		additionalSuffix:            targetAdditionalSuffix,
		nodeName:                    nodeName,
		profile:                     ms.ClusterProfile,
		config:                      config,
		params:                      params,
		env:                         ms.Environment,
		client:                      client,
		jobSpec:                     jobSpec,
		observers:                   ms.Observers,
		pre:                         ms.Pre,
		test:                        ms.Test,
		post:                        ms.Post,
		flags:                       flags,
		leases:                      leases,
		clusterClaim:                testConfig.ClusterClaim,
		subLock:                     &sync.Mutex{},
		cancelObservers:             cancelObservers,
		nodeArchitecture:            testConfig.NodeArchitecture,
		enableSecretsStoreCSIDriver: enableSecretsStoreCSIDriver,
	}
}

func (s *multiStageTestStep) profileSecretName() string {
	name := s.name
	if s.additionalSuffix != "" {
		name = strings.TrimSuffix(name, fmt.Sprintf("-%s", s.additionalSuffix))
	}
	return name + "-cluster-profile"
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
		if err := s.getProfileData(ctx); err != nil {
			return err
		}
	}
	env, err := s.environment()
	if err != nil {
		return err
	}
	if err := s.createSharedDirSecret(ctx); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	if s.enableSecretsStoreCSIDriver {
		if err := s.createSPCs(ctx); err != nil {
			return fmt.Errorf("failed to create SecretProviderClass objects: %w", err)
		}
	} else {
		if err := s.createCredentials(ctx); err != nil {
			return fmt.Errorf("failed to create credentials: %w", err)
		}
	}
	if err := s.createCommandConfigMaps(ctx); err != nil {
		return fmt.Errorf("failed to create command configmap: %w", err)
	}
	if err := s.setupRBAC(ctx); err != nil {
		return fmt.Errorf("failed to create RBAC objects: %w", err)
	}
	if s.vpnConf != nil {
		if s.vpnConf.namespaceUID, err = getNamespaceUID(ctx, s.jobSpec.Namespace(), s.client); err != nil {
			return fmt.Errorf("failed to determine namespace UID range: %w", err)
		}
	}
	secretVolumes, secretVolumeMounts, err := secretsForCensoring(s.client, s.jobSpec.Namespace(), ctx)
	if err != nil {
		return err
	}
	if s.enableSecretsStoreCSIDriver {
		secretVolumes, secretVolumeMounts = s.addCredentialsToCensoring(secretVolumes, secretVolumeMounts)
	}
	var errs []error
	generateObserverOpt := defaultGeneratePodOptions()
	generateObserverOpt.IsObserver = true
	generateObserverOpt.enableSecretsStoreCSIDriver = s.enableSecretsStoreCSIDriver
	observers, err := s.generateObservers(s.observers, secretVolumes, secretVolumeMounts, generateObserverOpt)
	if err != nil {
		// if we can't even generate the Pods there's no reason to run the job
		return err
	}
	observerContext, cancel := context.WithCancel(ctx)
	observerDone := make(chan struct{})
	go s.runObservers(observerContext, ctx, observers, observerDone)
	s.flags |= shortCircuit
	if err := s.runSteps(ctx, "pre", s.pre, env, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q pre steps failed: %w", s.name, err))
	} else if err := s.runSteps(ctx, "test", s.test, env, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q test steps failed: %w", s.name, err))
	}
	s.cancelObserversContext(cancel) // signal to observers that we're tearing down
	s.flags &= ^shortCircuit
	if err := s.runSteps(context.Background(), "post", s.post, env, secretVolumes, secretVolumeMounts); err != nil {
		errs = append(errs, fmt.Errorf("%q post steps failed: %w", s.name, err))
	}
	<-observerDone // wait for the observers to finish so we get their jUnit
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

// getProfileData fetches the content of the cluster profile secret.
// This is done both to guarantee it has been correctly imported into the test
// namespace and to gather information used when generating the test pods.
func (s *multiStageTestStep) getProfileData(ctx context.Context) error {
	var secret coreapi.Secret
	name := s.profileSecretName()
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: name}, &secret); err != nil {
		return fmt.Errorf("could not get cluster profile secret %q: %w", name, err)
	}
	if err := s.readVPNData(&secret); err != nil {
		return fmt.Errorf("failed to read VPN configuration from cluster profile: %w", err)
	}
	return nil
}

func (s *multiStageTestStep) readVPNData(secret *coreapi.Secret) error {
	bytes, ok := secret.Data[vpnConfPath]
	if !ok {
		return nil
	}
	var c vpnConf
	if err := yaml.UnmarshalStrict(bytes, &c); err != nil {
		return fmt.Errorf("failed to read VPN configuration file: %w", err)
	}
	if c.Image == "" {
		return fmt.Errorf("VPN image missing in configuration file")
	}
	if c.Commands == "" {
		return fmt.Errorf("VPN script missing in configuration file")
	}
	cmd, ok := secret.Data[c.Commands]
	if !ok {
		return fmt.Errorf(`invalid "commands" value %q, not found`, c.Commands)
	}
	c.Commands = string(cmd)
	if w := c.WaitTimeout; w != nil {
		var err error
		if _, err = time.ParseDuration(*w); err != nil {
			return fmt.Errorf("invalid VPN wait timeout %q: %w", *w, err)
		}
	}
	s.vpnConf = &c
	return nil
}

func (s *multiStageTestStep) environment() ([]coreapi.EnvVar, error) {
	var ret []coreapi.EnvVar
	if s.params == nil {
		return ret, nil
	}
	for _, l := range s.leases {
		val, err := s.params.Get(l.Env)
		if err != nil {
			return nil, err
		}
		ret = append(ret, coreapi.EnvVar{Name: l.Env, Value: val})
	}

	for _, name := range []string{api.InitialReleaseName, api.LatestReleaseName} {
		envVar := fmt.Sprintf("ORIGINAL_%s", utils.ReleaseImageEnv(name))
		pullspec, err := s.params.Get(envVar)
		if err != nil {
			return nil, err
		} else if pullspec != "" {
			ret = append(ret, coreapi.EnvVar{Name: envVar, Value: pullspec})
		}
	}

	if s.profile != "" {
		for _, e := range envForProfile {
			val, err := s.params.Get(e)
			if err != nil {
				return nil, err
			}
			ret = append(ret, coreapi.EnvVar{Name: e, Value: val})
		}
		if s.profile == "aws" { //TODO(sgoeddel): only enabled for aws for now, later this will be configurable
			val, err := s.params.Get(api.DefaultIPPoolLeaseEnv)
			if err != nil {
				return nil, err
			}
			ret = append(ret, coreapi.EnvVar{Name: api.DefaultIPPoolLeaseEnv, Value: val})
		}
	}
	return ret, nil
}

func (s *multiStageTestStep) cancelObserversContext(cancel context.CancelFunc) {
	if s.cancelObservers != nil {
		s.cancelObservers(cancel)
	} else {
		cancel()
	}
}

func (s *multiStageTestStep) ResolveMultiArch() sets.Set[string] {
	arch := string(api.NodeArchitectureAMD64)
	if s.nodeArchitecture != "" {
		arch = string(s.nodeArchitecture)
	}
	return sets.New[string](arch)
}

func (s *multiStageTestStep) AddArchitectures(archs []string) {
	// We don't want anything else rather than the node_architecture in the config
	// to add another architecture.
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
	i := 0
	for _, secret := range secretList.Items {
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
		i++
	}
	return secretVolumes, secretVolumeMounts, nil
}

func (s *multiStageTestStep) addCredentialsToCensoring(secretVolumes []coreapi.Volume, secretVolumeMounts []coreapi.VolumeMount) ([]coreapi.Volume, []coreapi.VolumeMount) {
	seenCredentials := make(map[string]bool)
	i := 0
	for _, step := range append(s.pre, append(s.test, s.post...)...) {
		for _, credential := range step.Credentials {
			if seenCredentials[credential.Name] {
				continue
			}
			seenCredentials[credential.Name] = true
			volumeName := fmt.Sprintf("censor-cred-%d", i)
			readOnly := true

			// Create individual SPC name for censoring - each credential
			// had its SPC already created in init.go's createSPCs function
			censorMountPath := getCensorMountPath(credential.Name)
			individualCredentials := []api.CredentialReference{credential}
			spcName := getSPCName(s.jobSpec.Namespace(), credential.Collection, censorMountPath, individualCredentials)

			secretVolumes = append(secretVolumes, coreapi.Volume{
				Name: volumeName,
				VolumeSource: coreapi.VolumeSource{
					CSI: &coreapi.CSIVolumeSource{
						Driver:   "secrets-store.csi.k8s.io",
						ReadOnly: &readOnly,
						VolumeAttributes: map[string]string{
							"secretProviderClass": spcName,
						},
					},
				},
			})
			secretVolumeMounts = append(secretVolumeMounts, coreapi.VolumeMount{
				Name:      volumeName,
				MountPath: getMountPath(credential.Name),
			})
			i++
		}
	}
	return secretVolumes, secretVolumeMounts
}

// getMountPath returns the mount path for a given secret name.
// This is used to get the path where the secrets to be censored
// will be mounted in the sidecar container.
func getMountPath(secretName string) string {
	return path.Join("/secrets", secretName)
}

func volumeName(ns, name string) string {
	return strings.ReplaceAll(fmt.Sprintf("%s-%s", ns, name), ".", "-")
}
