package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowio "sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/defaults"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/validation"
)

type stringSlice struct {
	values []string
}

func (s *stringSlice) String() string {
	return strings.Join(s.values, string(filepath.Separator))
}

func (s *stringSlice) Set(value string) error {
	s.values = append(s.values, value)
	return nil
}

type options struct {
	configSpecPath       string
	unresolvedConfigPath string
	secretDirectories    stringSlice
	sshKeyPath           string
	oauthTokenPath       string

	targets stringSlice
	promote bool

	verbose    bool
	help       bool
	printGraph bool

	writeParams string
	artifactDir string

	gitRef                 string
	namespace              string
	baseNamespace          string
	extraInputHash         stringSlice
	idleCleanupDuration    time.Duration
	idleCleanupDurationSet bool
	cleanupDuration        time.Duration
	cleanupDurationSet     bool

	inputHash                  string
	secrets                    []*coreapi.Secret
	graphConfig                api.GraphConfiguration
	configSpec                 *api.ReleaseBuildConfiguration
	jobSpec                    *api.JobSpec
	clusterConfig              *rest.Config
	podPendingTimeout          time.Duration
	consoleHost                string
	nodeName                   string
	leaseServer                string
	leaseServerCredentialsFile string
	leaseAcquireTimeout        time.Duration
	leaseClient                lease.Client
	clusterProfiles            []metrics.ClusterProfileForTarget

	givePrAuthorAccessToNamespace bool
	impersonateUser               string
	authors                       []string

	resolverAddress string
	resolverClient  server.ResolverClient

	registryPath string
	org          string
	repo         string
	branch       string
	variant      string

	injectTest string

	metadataRevision int

	pullSecretPath string
	pullSecret     *coreapi.Secret

	pushSecretPath string
	pushSecret     *coreapi.Secret

	uploadSecretPath string
	uploadSecret     *coreapi.Secret

	cloneAuthConfig *steps.CloneAuthConfig

	resultsOptions results.Options

	censor *secrets.DynamicCensor

	hiveKubeconfigPath string
	hiveKubeconfig     *rest.Config

	multiStageParamOverrides stringSlice
	dependencyOverrides      stringSlice

	targetAdditionalSuffix string
	manifestToolDockerCfg  string
	localRegistryDNS       string

	restrictNetworkAccess       bool
	enableSecretsStoreCSIDriver bool
	gsmConfigPath               string
	gsmConfig                   api.GSMConfig
	gsmProjectConfigPath        string
	gsmProjectConfig            gsm.Config
	gsmCredentialsFile          string

	metricsAgent *metrics.MetricsAgent

	skippedImages sets.Set[string]
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{
		idleCleanupDuration: 1 * time.Hour,
		cleanupDuration:     72 * time.Hour,
	}

	// command specific options
	flag.BoolVar(&opt.help, "h", false, "short for --help")
	flag.BoolVar(&opt.help, "help", false, "See help for this command.")
	flag.BoolVar(&opt.verbose, "v", false, "Show verbose output.")

	// what we will run
	flag.StringVar(&opt.nodeName, "node", "", "Restrict scheduling of pods to a single node in the cluster. Does not afffect indirectly created pods (e.g. builds).")
	flag.DurationVar(&opt.podPendingTimeout, "pod-pending-timeout", 60*time.Minute, "Maximum amount of time created pods can spend before the running state. For test pods, this applies to each container. For builds, it applies to the build execution as a whole.")
	flag.StringVar(&opt.leaseServer, "lease-server", leaseServerAddress, "Address of the server that manages leases. Required if any test is configured to acquire a lease.")
	flag.StringVar(&opt.leaseServerCredentialsFile, "lease-server-credentials-file", "", "The path to credentials file used to access the lease server. The content is of the form <username>:<password>.")
	flag.DurationVar(&opt.leaseAcquireTimeout, "lease-acquire-timeout", leaseAcquireTimeout, "Maximum amount of time to wait for lease acquisition")
	flag.StringVar(&opt.registryPath, "registry", "", "Path to the step registry directory")
	flag.StringVar(&opt.configSpecPath, "config", "", "The configuration file. If not specified the CONFIG_SPEC environment variable or the configresolver will be used.")
	flag.StringVar(&opt.unresolvedConfigPath, "unresolved-config", "", "The configuration file, before resolution. If not specified the UNRESOLVED_CONFIG environment variable will be used, if set.")
	flag.Var(&opt.targets, "target", "One or more targets in the configuration to build. Only steps that are required for this target will be run.")
	flag.BoolVar(&opt.printGraph, "print-graph", opt.printGraph, "Print a directed graph of the build steps and exit. Intended for use with the golang digraph utility.")

	// add to the graph of things we run or create
	flag.Var(&opt.secretDirectories, "secret-dir", "One or more directories that should converted into secrets in the test namespace. If the directory contains a single file with name .dockercfg or config.json it becomes a pull secret.")
	flag.StringVar(&opt.sshKeyPath, "ssh-key-path", "", "A path of the private ssh key that is going to be used to clone a private repository.")
	flag.StringVar(&opt.oauthTokenPath, "oauth-token-path", "", "A path of the OAuth token that is going to be used to clone a private repository.")

	// the target namespace and cleanup behavior
	flag.Var(&opt.extraInputHash, "input-hash", "Add arbitrary inputs to the build input hash to make the created namespace unique.")
	flag.StringVar(&opt.namespace, "namespace", "", "Namespace to create builds into, defaults to build_id from JOB_SPEC. If the string '{id}' is in this value it will be replaced with the build input hash.")
	flag.StringVar(&opt.baseNamespace, "base-namespace", "stable", "Namespace to read builds from, defaults to stable.")
	flag.DurationVar(&opt.idleCleanupDuration, "delete-when-idle", opt.idleCleanupDuration, "If no pod is running for longer than this interval, delete the namespace. Set to zero to retain the contents. Requires the namespace TTL controller to be deployed.")
	flag.DurationVar(&opt.cleanupDuration, "delete-after", opt.cleanupDuration, "If namespace exists for longer than this interval, delete the namespace. Set to zero to retain the contents. Requires the namespace TTL controller to be deployed.")

	// actions to add to the graph
	flag.BoolVar(&opt.promote, "promote", false, "When all other targets complete, publish the set of images built by this job into the release configuration.")

	// output control
	flag.StringVar(&opt.artifactDir, "artifact-dir", "", "DEPRECATED. Does nothing, set $ARTIFACTS instead.")
	flag.StringVar(&opt.writeParams, "write-params", "", "If set write an env-compatible file with the output of the job.")

	// experimental flags
	flag.StringVar(&opt.gitRef, "git-ref", "", "Populate the job spec from this local Git reference. If JOB_SPEC is set, the refs field will be overwritten.")
	flag.BoolVar(&opt.givePrAuthorAccessToNamespace, "give-pr-author-access-to-namespace", true, "Give view access to the temporarily created namespace to the PR author.")
	flag.StringVar(&opt.impersonateUser, "as", "", "Username to impersonate")
	flag.BoolVar(&opt.restrictNetworkAccess, "restrict-network-access", false, "Restrict network access to 10.0.0.0/8 (RedHat intranet).")
	flag.BoolVar(&opt.enableSecretsStoreCSIDriver, "enable-secrets-store-csi-driver", false, "Use Secrets Store CSI driver for accessing multi-stage credentials.")

	// Google Secret Manager flags
	flag.StringVar(&opt.gsmConfigPath, "gsm-config", "", "Path to the gsm secrets config file.")
	flag.StringVar(&opt.gsmProjectConfigPath, "gsm-project-config", "", "Path to the GSM project config file.")
	flag.StringVar(&opt.gsmCredentialsFile, "gsm-credentials-file", "", "Path to GCP service account credentials.")

	// flags needed for the configresolver
	flag.StringVar(&opt.resolverAddress, "resolver-address", configResolverAddress, "Address of configresolver")
	flag.StringVar(&opt.org, "org", "", "Org of the project (used by configresolver)")
	flag.StringVar(&opt.repo, "repo", "", "Repo of the project (used by configresolver)")
	flag.StringVar(&opt.branch, "branch", "", "Branch of the project (used by configresolver)")
	flag.StringVar(&opt.variant, "variant", "", "Variant of the project's ci-operator config (used by configresolver)")

	flag.StringVar(&opt.injectTest, "with-test-from", "", "Inject a test from another ci-operator config, specified by ORG/REPO@BRANCH{__VARIANT}:TEST or JSON (used by configresolver)")

	flag.StringVar(&opt.pullSecretPath, "image-import-pull-secret", "", "A set of dockercfg credentials used to import images for the tag_specification.")
	flag.StringVar(&opt.pushSecretPath, "image-mirror-push-secret", "", "A set of dockercfg credentials used to mirror images for the promotion.")
	flag.StringVar(&opt.uploadSecretPath, "gcs-upload-secret", "", "GCS credentials used to upload logs and artifacts.")

	flag.StringVar(&opt.hiveKubeconfigPath, "hive-kubeconfig", "", "Path to the kubeconfig file to use for requests to Hive.")

	flag.Var(&opt.multiStageParamOverrides, "multi-stage-param", "A repeatable option where one or more environment parameters can be passed down to the multi-stage steps. This parameter should be in the format NAME=VAL. e.g --multi-stage-param PARAM1=VAL1 --multi-stage-param PARAM2=VAL2.")
	flag.Var(&opt.dependencyOverrides, "dependency-override-param", "A repeatable option used to override dependencies with external pull specs. This parameter should be in the format ENVVARNAME=PULLSPEC, e.g. --dependency-override-param=OO_INDEX=registry.mydomain.com:5000/pushed/myimage. This would override the value for the OO_INDEX environment variable for any tests/steps that currently have that dependency configured.")

	flag.StringVar(&opt.targetAdditionalSuffix, "target-additional-suffix", "", "Inject an additional suffix onto the targeted test's 'as' name. Used for adding an aggregate index")

	flag.StringVar(&opt.manifestToolDockerCfg, "manifest-tool-dockercfg", "/secrets/manifest-tool/.dockerconfigjson", "The dockercfg file path to be used to push the manifest listed image after build. This is being used by the manifest-tool binary.")
	flag.StringVar(&opt.localRegistryDNS, "local-registry-dns", "image-registry.openshift-image-registry.svc:5000", "Defines the target image registry.")

	opt.resultsOptions.Bind(flag)
	return opt
}

func (o *options) Complete() error {
	jobSpec, err := api.ResolveSpecFromEnv()
	if err != nil {
		if len(o.gitRef) == 0 {
			return fmt.Errorf("failed to determine job spec: no --git-ref passed and failed to resolve job spec from env: %w", err)
		}
		// Failed to read $JOB_SPEC but --git-ref was passed, so try that instead
		spec, refErr := jobSpecFromGitRef(o.gitRef)
		if refErr != nil {
			return fmt.Errorf("failed to determine job spec: failed to resolve --git-ref: %w", refErr)
		}
		jobSpec = spec
	} else if len(o.gitRef) > 0 {
		// Read from $JOB_SPEC but --git-ref was also passed, so merge them
		spec, err := jobSpecFromGitRef(o.gitRef)
		if err != nil {
			return fmt.Errorf("failed to determine job spec: failed to resolve --git-ref: %w", err)
		}
		jobSpec.Refs = spec.Refs
	}
	jobSpec.BaseNamespace = o.baseNamespace
	target := "all"
	if len(o.targets.values) > 0 {
		target = o.targets.values[0]
	}
	o.jobSpec = jobSpec
	o.jobSpec.Target = target

	info := o.getResolverInfo(jobSpec)
	o.resolverClient = server.NewResolverClient(o.resolverAddress)

	if o.unresolvedConfigPath != "" && o.configSpecPath != "" {
		return errors.New("cannot set --config and --unresolved-config at the same time")
	}
	if o.unresolvedConfigPath != "" && o.resolverAddress == "" {
		return errors.New("cannot request resolved config with --unresolved-config unless providing --resolver-address")
	}

	if o.enableSecretsStoreCSIDriver && o.gsmConfigPath == "" {
		return fmt.Errorf("--gsm-config is required when --enable-secrets-store-csi-driver is enabled")
	}

	injectTest, err := o.getInjectTest()
	if err != nil {
		return err
	}

	var config *api.ReleaseBuildConfiguration
	if injectTest != nil {
		if o.resolverAddress == "" {
			return errors.New("cannot request config with injected test without providing --resolver-address")
		}
		if o.unresolvedConfigPath != "" || o.configSpecPath != "" {
			return errors.New("cannot request injecting test into locally provided config")
		}
		config, err = o.resolverClient.ConfigWithTest(info, injectTest)
	} else {
		var opener prowio.Opener
		if _, set := os.LookupEnv(configSpecGcsUrlVar); set { // The opener is only needed when we may have to read from a GCS bucket
			opener, err = prowio.NewOpener(context.Background(), o.uploadSecretPath, "")
			if err != nil {
				logrus.WithError(err).Fatalf("Error creating opener to read %s", configSpecGcsUrlVar)
			}
		}
		config, err = o.loadConfig(info, bucketReader{opener: opener})
	}

	if err != nil {
		return results.ForReason("loading_config").WithError(err).Errorf("failed to load configuration: %v", err)
	}

	if len(o.gitRef) != 0 && config.CanonicalGoRepository != nil {
		o.jobSpec.Refs.PathAlias = *config.CanonicalGoRepository
	}
	o.configSpec = config
	o.jobSpec.Metadata = config.Metadata
	mergedConfig := o.injectTest != ""
	if err := validation.IsValidResolvedConfiguration(o.configSpec, mergedConfig); err != nil {
		return results.ForReason("validating_config").ForError(err)
	}
	o.skippedImages = determineSkippedImages(o.configSpec, o.jobSpec, o.targets.values)
	o.graphConfig = defaults.FromConfigStatic(o.configSpec)
	if err := validation.IsValidGraphConfiguration(o.graphConfig.Steps); err != nil {
		return results.ForReason("validating_config").ForError(err)
	}

	if o.verbose {
		config, _ := yaml.Marshal(o.configSpec)
		logrus.WithField("config", string(config)).Trace("Resolved configuration.")
		job, _ := json.Marshal(o.jobSpec)
		logrus.WithField("jobspec", string(job)).Trace("Resolved job spec.")
	}

	var refs []prowapi.Refs
	if o.jobSpec.Refs != nil {
		refs = append(refs, *o.jobSpec.Refs)
	}
	refs = append(refs, o.jobSpec.ExtraRefs...)

	if len(refs) == 0 {
		logrus.Info("No source defined")
	}
	for _, ref := range refs {
		if ref.BaseSHA == "" {
			logrus.Debugf("Resolved SHA missing for %s in https://github.com/%s/%s: adding synthetic input to avoid false cache hit", ref.BaseRef, ref.Org, ref.Repo)
			o.extraInputHash.values = append(o.extraInputHash.values, time.Now().String())
		}
		logrus.Info(summarizeRef(ref))

		for _, pull := range ref.Pulls {
			o.authors = append(o.authors, pull.Author)
		}
	}

	if len(o.sshKeyPath) > 0 && len(o.oauthTokenPath) > 0 {
		return errors.New("both --ssh-key-path and --oauth-token-path are specified")
	}

	var cloneAuthSecretPath string
	if len(o.oauthTokenPath) > 0 {
		cloneAuthSecretPath = o.oauthTokenPath
		o.cloneAuthConfig = &steps.CloneAuthConfig{Type: steps.CloneAuthTypeOAuth}
	} else if len(o.sshKeyPath) > 0 {
		cloneAuthSecretPath = o.sshKeyPath
		o.cloneAuthConfig = &steps.CloneAuthConfig{Type: steps.CloneAuthTypeSSH}
	}

	if len(cloneAuthSecretPath) > 0 {
		o.cloneAuthConfig.Secret, err = getCloneSecretFromPath(o.cloneAuthConfig.Type, cloneAuthSecretPath)
		if err != nil {
			return fmt.Errorf("could not get secret from path %s: %w", cloneAuthSecretPath, err)
		}
	}

	for _, path := range o.secretDirectories.values {
		secret, err := util.SecretFromDir(path)
		name := filepath.Base(path)
		if err != nil {
			return fmt.Errorf("failed to generate secret %s: %w", name, err)
		}
		secret.Name = name
		if len(secret.Data) == 1 {
			if _, ok := secret.Data[coreapi.DockerConfigJsonKey]; ok {
				secret.Type = coreapi.SecretTypeDockerConfigJson
			}
			if _, ok := secret.Data[coreapi.DockerConfigKey]; ok {
				secret.Type = coreapi.SecretTypeDockercfg
			}
		}
		o.secrets = append(o.secrets, secret)
	}

	o.getClusterProfileNamesFromTargets()

	clusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to load cluster config: %w", err)
	}

	if len(o.impersonateUser) > 0 {
		clusterConfig.Impersonate = rest.ImpersonationConfig{UserName: o.impersonateUser}
	}

	if o.verbose {
		clusterConfig.ContentType = "application/json"
		clusterConfig.AcceptContentTypes = "application/json"
	}

	o.clusterConfig = clusterConfig

	if o.pullSecretPath != "" {
		if o.pullSecret, err = getDockerConfigSecret(api.RegistryPullCredentialsSecret, o.pullSecretPath); err != nil {
			return fmt.Errorf("could not get pull secret %s from path %s: %w", api.RegistryPullCredentialsSecret, o.pullSecretPath, err)
		}
	}
	if o.pushSecretPath != "" {
		if o.pushSecret, err = getDockerConfigSecret(api.RegistryPushCredentialsCICentralSecret, o.pushSecretPath); err != nil {
			return fmt.Errorf("could not get push secret %s from path %s: %w", api.RegistryPushCredentialsCICentralSecret, o.pushSecretPath, err)
		}
	}

	if o.uploadSecretPath != "" {
		gcsSecretName := resolveGCSCredentialsSecret(o.jobSpec)
		if o.uploadSecret, err = getSecret(gcsSecretName, o.uploadSecretPath); err != nil {
			return fmt.Errorf("could not get upload secret %s from path %s: %w", gcsSecretName, o.uploadSecretPath, err)
		}
	}

	if o.hiveKubeconfigPath != "" {
		kubeConfig, err := util.LoadKubeConfig(o.hiveKubeconfigPath)
		if err != nil {
			return fmt.Errorf("could not load Hive kube config from path %s: %w", o.hiveKubeconfigPath, err)
		}
		o.hiveKubeconfig = kubeConfig
	}

	applyEnvOverrides(o)

	if err := overrideMultiStageParams(o); err != nil {
		return err
	}

	handleTargetAdditionalSuffix(o)
	setDisablePodScalerFromTarget(o)

	if o.enableSecretsStoreCSIDriver {
		if err := api.LoadGSMConfigFromFile(o.gsmConfigPath, &o.gsmConfig); err != nil {
			return err
		}
		if err = api.LoadGSMProjectConfigFromFile(o.gsmProjectConfigPath, &o.gsmProjectConfig); err != nil {
			return err
		}
	}

	return overrideTestStepDependencyParams(o)
}

func (o *options) ToGraphConfig() *defaults.Config {
	return &defaults.Config{
		Clients: defaults.Clients{
			LeaseClientEnabled: o.isLeaseClientAvailable(),
		},
		CIConfig:               o.configSpec,
		GraphConf:              &o.graphConfig,
		JobSpec:                o.jobSpec,
		ParamFile:              o.writeParams,
		Promote:                o.promote,
		ClusterConfig:          o.clusterConfig,
		PodPendingTimeout:      o.podPendingTimeout,
		RequiredTargets:        o.targets.values,
		CloneAuthConfig:        o.cloneAuthConfig,
		PullSecret:             o.pullSecret,
		PushSecret:             o.pushSecret,
		Censor:                 o.censor,
		HiveKubeconfig:         o.hiveKubeconfig,
		NodeName:               o.nodeName,
		TargetAdditionalSuffix: o.targetAdditionalSuffix,
		ManifestToolDockerCfg:  o.manifestToolDockerCfg,
		LocalRegistryDNS:       o.localRegistryDNS,
		MetricsAgent:           o.metricsAgent,
		SkippedImages:          o.skippedImages,
		ClusterProfileGetter:   o.resolverClient.ClusterProfile,
	}
}

func (o *options) resolveConsoleHost() {
	if client, err := ctrlruntimeclient.New(o.clusterConfig, ctrlruntimeclient.Options{}); err != nil {
		logrus.WithError(err).Warn("Could not create client for accessing Routes. Will not resolve console URL.")
	} else {
		host, err := api.ResolveConsoleHost(context.TODO(), client)
		if err != nil {
			logrus.WithError(err).Warn("Could not resolve OpenShift console host. Will not resolve console URL.")
		} else {
			o.consoleHost = host
		}
	}
}

func (o *options) getResolverInfo(jobSpec *api.JobSpec) *api.Metadata {
	// address and variant can only be set via options
	info := &api.Metadata{Variant: o.variant}

	allRefs := jobSpec.ExtraRefs
	if jobSpec.Refs != nil {
		allRefs = append([]prowapi.Refs{*jobSpec.Refs}, allRefs...)
	}

	// identify org, repo, and branch from refs object
	for i, ref := range allRefs {
		if ref.Org != "" && ref.Repo != "" && ref.BaseRef != "" {
			info.Org += fmt.Sprintf("%s,", ref.Org)
			info.Repo += fmt.Sprintf("%s,", ref.Repo)
			info.Branch += fmt.Sprintf("%s,", ref.BaseRef)
			if info.Variant != "" && i > 0 {
				info.Variant = fmt.Sprintf("%s,", info.Variant)
			}
		}
	}
	info.Org = strings.TrimSuffix(info.Org, ",")
	info.Repo = strings.TrimSuffix(info.Repo, ",")
	info.Branch = strings.TrimSuffix(info.Branch, ",")

	// if flags set, override previous values
	if o.org != "" {
		info.Org = o.org
	}
	if o.repo != "" {
		info.Repo = o.repo
	}
	if o.branch != "" {
		info.Branch = o.branch
	}
	return info
}

func (o *options) getInjectTest() (*api.MetadataWithTest, error) {
	if o.injectTest == "" {
		return nil, nil
	}
	var ret api.MetadataWithTest
	if err := json.Unmarshal([]byte(o.injectTest), &ret); err == nil {
		return &ret, nil
	}

	return api.MetadataTestFromString(o.injectTest)
}

// getClusterProfileNamesFromTargets extracts the needed cluster profile name(s) from the target arg(s)
func (o *options) getClusterProfileNamesFromTargets() {
	for _, targetName := range o.targets.values {
		for _, test := range o.configSpec.Tests {
			if targetName != test.As {
				continue
			}
			profile := test.GetClusterProfileName()
			if profile != "" {
				o.clusterProfiles = append(o.clusterProfiles, metrics.ClusterProfileForTarget{Target: test.As, ProfileName: profile})
			}
		}
	}
}
