package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/decorate"
	"sigs.k8s.io/yaml"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	templateapi "github.com/openshift/api/template/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/configresolver"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/official"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/clusterinstall"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/multi_stage"
	releasesteps "github.com/openshift/ci-tools/pkg/steps/release"
	"github.com/openshift/ci-tools/pkg/steps/secretrecordingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

type inputImageSet map[api.InputImage]struct{}

// FromConfig generates the final execution graph.
// It interprets the human-friendly fields in the release build configuration
// and pre-parsed graph configuration and generates steps for them, returning
// the full set of steps requires for the build, including defaulted steps,
// generated steps and all raw steps that the user provided.
func FromConfig(
	ctx context.Context,
	config *api.ReleaseBuildConfiguration,
	graphConf *api.GraphConfiguration,
	jobSpec *api.JobSpec,
	templates []*templateapi.Template,
	paramFile string,
	promote bool,
	clusterConfig *rest.Config,
	podPendingTimeout time.Duration,
	leaseClient *lease.Client,
	requiredTargets []string,
	cloneAuthConfig *steps.CloneAuthConfig,
	pullSecret, pushSecret *coreapi.Secret,
	censor *secrets.DynamicCensor,
	hiveKubeconfig *rest.Config,
	consoleHost string,
	nodeName string,
	nodeArchitectures []string,
	targetAdditionalSuffix string,
	manifestToolDockerCfg string,
	localRegistryDNS string,
	integratedStreams map[string]*configresolver.IntegratedStream,
) ([]api.Step, []api.Step, error) {
	crclient, err := ctrlruntimeclient.NewWithWatch(clusterConfig, ctrlruntimeclient.Options{})
	crclient = secretrecordingclient.Wrap(crclient, censor)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to construct client: %w", err)
	}
	client := loggingclient.New(crclient)
	buildGetter, err := buildclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get build client for cluster config: %w", err)
	}
	buildClient := steps.NewBuildClient(client, buildGetter.RESTClient(), nodeArchitectures, manifestToolDockerCfg, localRegistryDNS)

	templateGetter, err := templateclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get template client for cluster config: %w", err)
	}
	templateClient := steps.NewTemplateClient(client, templateGetter.RESTClient())

	coreGetter, err := coreclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get core client for cluster config: %w", err)
	}

	podClient := kubernetes.NewPodClient(client, clusterConfig, coreGetter.RESTClient(), podPendingTimeout)

	var hiveClient ctrlruntimeclient.WithWatch
	if hiveKubeconfig != nil {
		hiveClient, err = ctrlruntimeclient.NewWithWatch(hiveKubeconfig, ctrlruntimeclient.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("could not get Hive client for Hive kube config: %w", err)
		}
	}
	httpClient := retryablehttp.NewClient()
	httpClient.Logger = nil

	return fromConfig(ctx, config, graphConf, jobSpec, templates, paramFile, promote, client, buildClient, templateClient, podClient, leaseClient, hiveClient, httpClient.StandardClient(), requiredTargets, cloneAuthConfig, pullSecret, pushSecret, api.NewDeferredParameters(nil), censor, consoleHost, nodeName, targetAdditionalSuffix, nodeArchitectures, integratedStreams)
}

func fromConfig(
	ctx context.Context,
	config *api.ReleaseBuildConfiguration,
	graphConf *api.GraphConfiguration,
	jobSpec *api.JobSpec,
	templates []*templateapi.Template,
	paramFile string,
	promote bool,
	client loggingclient.LoggingClient,
	buildClient steps.BuildClient,
	templateClient steps.TemplateClient,
	podClient kubernetes.PodClient,
	leaseClient *lease.Client,
	hiveClient ctrlruntimeclient.WithWatch,
	httpClient release.HTTPClient,
	requiredTargets []string,
	cloneAuthConfig *steps.CloneAuthConfig,
	pullSecret, pushSecret *coreapi.Secret,
	params *api.DeferredParameters,
	censor *secrets.DynamicCensor,
	consoleHost string,
	nodeName string,
	targetAdditionalSuffix string,
	nodeArchitectures []string,
	integratedStreams map[string]*configresolver.IntegratedStream,
) ([]api.Step, []api.Step, error) {
	requiredNames := sets.New[string]()
	for _, target := range requiredTargets {
		requiredNames.Insert(target)
	}
	params.Add("JOB_NAME", func() (string, error) { return jobSpec.Job, nil })
	params.Add("JOB_NAME_HASH", func() (string, error) { return jobSpec.JobNameHash(), nil })
	params.Add("JOB_NAME_SAFE", func() (string, error) { return strings.Replace(jobSpec.Job, "_", "-", -1), nil })
	params.Add("UNIQUE_HASH", func() (string, error) { return jobSpec.UniqueHash(), nil })
	params.Add("NAMESPACE", func() (string, error) { return jobSpec.Namespace(), nil })
	// when provided, RELEASE_IMAGE_INITIAL and RELASE_IMAGE_LATEST will be overwritten when resolving the respective releases.
	// some multi-stage steps need the original values of these env vars, so we set them on respective ORIGINAL_* vars
	for _, name := range []string{api.InitialReleaseName, api.LatestReleaseName} {
		envVar := utils.ReleaseImageEnv(name)
		val, err := params.Get(envVar)
		if err != nil {
			logrus.WithError(err).Warnf("couldn't get env var for: %s", name)
		} else if val != "" {
			logrus.Debugf("setting original value of overridden release image to env var for: %s", name)
			params.Add(fmt.Sprintf("ORIGINAL_%s", envVar), func() (string, error) { return val, nil })
		}
	}
	inputImages := make(inputImageSet)
	var overridableSteps []api.Step
	var buildSteps []api.Step
	var imageStepLinks []api.StepLink
	var hasReleaseStep bool
	resolver := rootImageResolver(client, ctx, promote)
	imageConfigs := graphConf.InputImages()
	rawSteps, err := runtimeStepConfigsForBuild(config, jobSpec, os.ReadFile, resolver, imageConfigs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get steps from configuration: %w", err)
	}
	rawSteps = append(graphConf.Steps, rawSteps...)
	rawSteps = append(rawSteps, stepsForImageOverrides(ctx, utils.GetOverriddenImages(), consoleHost, client, time.Second)...)

	for _, rawStep := range rawSteps {
		if testStep := rawStep.TestStepConfiguration; testStep != nil {
			steps, err := stepForTest(config, params, podClient, leaseClient, templateClient, client, hiveClient, jobSpec, inputImages, testStep, &imageConfigs, pullSecret, censor, nodeName, targetAdditionalSuffix)
			if err != nil {
				return nil, nil, err
			}
			buildSteps = append(buildSteps, steps...)
			continue
		}
		if resolveConfig := rawStep.ResolvedReleaseImagesStepConfiguration; resolveConfig != nil {
			// we need to expose the release step as 'step' so that it's in the
			// graph and can be targeted with '--target', but we can't let it get
			// removed via env-var, since release steps are apparently not subject
			// to that mechanism ...
			//
			// this is a disgusting hack but the simplest implementation until we
			// factor release steps into something more reusable
			hasReleaseStep = true
			var value string
			var overrideCLIReleaseExtractImage *coreapi.ObjectReference
			var overrideCLIResolveErr error
			switch {
			case resolveConfig.Integration != nil:
				overrideCLIReleaseExtractImage, overrideCLIResolveErr = resolveCLIOverrideImage(api.ReleaseArchitectureAMD64, resolveConfig.Integration.Name)
			case resolveConfig.Candidate != nil:
				overrideCLIReleaseExtractImage, overrideCLIResolveErr = resolveCLIOverrideImage(resolveConfig.Candidate.Architecture, resolveConfig.Candidate.Version)
			case resolveConfig.Release != nil:
				overrideCLIReleaseExtractImage, overrideCLIResolveErr = resolveCLIOverrideImage(resolveConfig.Release.Architecture, resolveConfig.Release.Version)
			case resolveConfig.Prerelease != nil:
				overrideCLIReleaseExtractImage, overrideCLIResolveErr = resolveCLIOverrideImage(resolveConfig.Prerelease.Architecture, resolveConfig.Prerelease.VersionBounds.Lower)
			}
			if overrideCLIResolveErr != nil {
				return nil, nil, results.ForReason("resolving_cli_override").ForError(fmt.Errorf("failed to resolve override CLI image for release %s: %w", resolveConfig.Name, overrideCLIResolveErr))
			}
			var source releasesteps.ReleaseSource
			if env := utils.ReleaseImageEnv(resolveConfig.Name); params.HasInput(env) {
				value, err = params.Get(env)
				if err != nil {
					return nil, nil, results.ForReason("resolving_release").ForError(fmt.Errorf("failed to get %q parameter: %w", env, err))
				}
				logrus.Infof("Using explicitly provided pull-spec for release %s (%s)", resolveConfig.Name, value)
				source = releasesteps.NewReleaseSourceFromPullSpec(value)
			} else {
				switch {
				case resolveConfig.Integration != nil:
					logrus.Infof("Building release %s from a snapshot of %s/%s", resolveConfig.Name, resolveConfig.Integration.Namespace, resolveConfig.Integration.Name)
					// this is the one case where we're not importing a payload, we need to get the images and build one
					snapshot := releasesteps.ReleaseSnapshotStep(resolveConfig.Name, *resolveConfig.Integration, podClient, jobSpec, integratedStreams[fmt.Sprintf("%s/%s", resolveConfig.Integration.Namespace, resolveConfig.Integration.Name)])
					assemble := releasesteps.AssembleReleaseStep(resolveConfig.Name, nodeName, &api.ReleaseTagConfiguration{
						Namespace:          resolveConfig.Integration.Namespace,
						Name:               resolveConfig.Integration.Name,
						IncludeBuiltImages: resolveConfig.Integration.IncludeBuiltImages,
					}, config.Resources, podClient, jobSpec)
					for _, s := range []api.Step{snapshot, assemble} {
						buildSteps = append(buildSteps, s)
						addProvidesForStep(s, params)
					}
					imageStepLinks = append(imageStepLinks, snapshot.Creates()...)
					continue
				default:
					source = releasesteps.NewReleaseSourceFromConfig(resolveConfig, httpClient)
				}
			}
			step := releasesteps.ImportReleaseStep(resolveConfig.Name, nodeName, resolveConfig.TargetName(), source, false, config.Resources, podClient, jobSpec, pullSecret, overrideCLIReleaseExtractImage)
			buildSteps = append(buildSteps, step)
			addProvidesForStep(step, params)
			continue
		}
		var step api.Step
		var stepLinks []api.StepLink
		if rawStep.InputImageTagStepConfiguration != nil {
			conf := *rawStep.InputImageTagStepConfiguration
			if _, ok := inputImages[conf.InputImage]; ok {
				continue
			}

			step = steps.InputImageTagStep(&conf, client, jobSpec)
			inputImages[conf.InputImage] = struct{}{}
		} else if rawStep.PipelineImageCacheStepConfiguration != nil {
			step = steps.PipelineImageCacheStep(*rawStep.PipelineImageCacheStepConfiguration, config.Resources, buildClient, podClient, jobSpec, pullSecret)
		} else if rawStep.SourceStepConfiguration != nil {
			step = steps.SourceStep(*rawStep.SourceStepConfiguration, config.Resources, buildClient, podClient, jobSpec, cloneAuthConfig, pullSecret)
		} else if rawStep.BundleSourceStepConfiguration != nil {
			step = steps.BundleSourceStep(*rawStep.BundleSourceStepConfiguration, config, config.Resources, buildClient, podClient, jobSpec, pullSecret)
		} else if rawStep.IndexGeneratorStepConfiguration != nil {
			step = steps.IndexGeneratorStep(*rawStep.IndexGeneratorStepConfiguration, config, config.Resources, buildClient, podClient, jobSpec, pullSecret)
		} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
			step = steps.ProjectDirectoryImageBuildStep(*rawStep.ProjectDirectoryImageBuildStepConfiguration, config, config.Resources, buildClient, podClient, jobSpec, pullSecret)
		} else if rawStep.ProjectDirectoryImageBuildInputs != nil {
			step = steps.GitSourceStep(*rawStep.ProjectDirectoryImageBuildInputs, config.Resources, buildClient, podClient, jobSpec, cloneAuthConfig, pullSecret)
		} else if rawStep.RPMImageInjectionStepConfiguration != nil {
			step = steps.RPMImageInjectionStep(*rawStep.RPMImageInjectionStepConfiguration, config.Resources, buildClient, podClient, jobSpec, pullSecret)
		} else if rawStep.RPMServeStepConfiguration != nil {
			step = steps.RPMServerStep(*rawStep.RPMServeStepConfiguration, client, jobSpec)
		} else if rawStep.OutputImageTagStepConfiguration != nil {
			step = steps.OutputImageTagStep(*rawStep.OutputImageTagStepConfiguration, client, jobSpec)
			// all required or non-optional output images are considered part of [images]
			if requiredNames.Has(string(rawStep.OutputImageTagStepConfiguration.From)) || !rawStep.OutputImageTagStepConfiguration.Optional {
				stepLinks = append(stepLinks, step.Creates()...)
			}
		} else if rawStep.ReleaseImagesTagStepConfiguration != nil {
			// if the user has specified a tag_specification we always
			// will import those images to the stable stream
			step = releasesteps.ReleaseImagesTagStep(*rawStep.ReleaseImagesTagStepConfiguration, client, params, jobSpec, integratedStreams[fmt.Sprintf("%s/%s", rawStep.ReleaseImagesTagStepConfiguration.Namespace, rawStep.ReleaseImagesTagStepConfiguration.Name)])
			stepLinks = append(stepLinks, step.Creates()...)

			hasReleaseStep = true

			// However, this user may have specified $RELEASE_IMAGE_foo
			// as well. For backwards compatibility, we explicitly support
			// 'initial' and 'latest': if not provided, we will build them.
			// If a pull spec was provided, however, it will be used.
			for _, name := range []string{api.InitialReleaseName, api.LatestReleaseName} {
				var releaseStep api.Step
				envVar := utils.ReleaseImageEnv(name)
				if params.HasInput(envVar) {
					pullSpec, err := params.Get(envVar)
					if err != nil {
						return nil, nil, results.ForReason("reading_release").ForError(fmt.Errorf("failed to read input release pullSpec %s: %w", name, err))
					}
					logrus.Infof("Using explicitly provided pull-spec for release %s (%s)", name, pullSpec)
					target := rawStep.ReleaseImagesTagStepConfiguration.TargetName(name)
					source := releasesteps.NewReleaseSourceFromPullSpec(pullSpec)
					releaseStep = releasesteps.ImportReleaseStep(name, nodeName, target, source, true, config.Resources, podClient, jobSpec, pullSecret, nil)
				} else {
					// for backwards compatibility, users get inclusion for free with tag_spec
					cfg := *rawStep.ReleaseImagesTagStepConfiguration
					cfg.IncludeBuiltImages = name == api.LatestReleaseName
					releaseStep = releasesteps.AssembleReleaseStep(name, nodeName, &cfg, config.Resources, podClient, jobSpec)
				}
				overridableSteps = append(overridableSteps, releaseStep)
				addProvidesForStep(releaseStep, params)
			}
		}
		step, ok := checkForFullyQualifiedStep(step, params)
		if ok {
			logrus.Infof("Task %s is satisfied by environment variables and will be skipped", step.Name())
		} else {
			imageStepLinks = append(imageStepLinks, stepLinks...)
		}
		overridableSteps = append(overridableSteps, step)
	}

	for _, template := range templates {
		step := steps.TemplateExecutionStep(template, params, podClient, templateClient, jobSpec, config.Resources)
		var hasClusterType, hasUseLease bool
		for _, p := range template.Parameters {
			hasClusterType = hasClusterType || p.Name == "CLUSTER_TYPE"
			hasUseLease = hasUseLease || p.Name == "USE_LEASE_CLIENT"
			if hasClusterType && hasUseLease {
				clusterType, err := params.Get("CLUSTER_TYPE")
				if err != nil {
					return nil, nil, fmt.Errorf("failed to get \"CLUSTER_TYPE\" parameter: %w", err)
				}
				lease, err := api.LeaseTypeFromClusterType(clusterType)
				if err != nil {
					return nil, nil, fmt.Errorf("cannot resolve lease type from cluster type: %w", err)
				}
				leases := []api.StepLease{{
					ResourceType: lease,
					Env:          api.DefaultLeaseEnv,
					Count:        1,
				}}
				step = steps.LeaseStep(leaseClient, leases, step, jobSpec.Namespace)
				break
			}
		}
		buildSteps = append(buildSteps, step)
		addProvidesForStep(step, params)
	}

	if len(paramFile) > 0 {
		step := steps.WriteParametersStep(params, paramFile)
		buildSteps = append(buildSteps, step)
		addProvidesForStep(step, params)
	}

	if !hasReleaseStep {
		step := releasesteps.StableImagesTagStep(client, jobSpec)
		buildSteps = append(buildSteps, step)
		addProvidesForStep(step, params)
	}

	step := steps.ImagesReadyStep(imageStepLinks)
	buildSteps = append(buildSteps, step)
	addProvidesForStep(step, params)

	var promotionSteps []api.Step
	if promote {
		if pushSecret == nil {
			return nil, nil, errors.New("--image-mirror-push-secret is required for promoting images")
		}
		if config.PromotionConfiguration == nil {
			return nil, nil, fmt.Errorf("cannot promote images, no promotion configuration defined")
		}

		promotionSteps = append(promotionSteps, releasesteps.PromotionStep(api.PromotionStepName, config, requiredNames, jobSpec, podClient, pushSecret, registryDomain(config.PromotionConfiguration), api.DefaultMirrorFunc, api.DefaultTargetNameFunc, nodeArchitectures))
		// Used primarily (only?) by the ci-chat-bot
		if config.PromotionConfiguration.RegistryOverride != "" {
			logrus.Info("No images to promote to quay.io if the registry is overridden")
		} else {
			promotionSteps = append(promotionSteps, releasesteps.PromotionStep(api.PromotionQuayStepName, config, requiredNames, jobSpec, podClient, pushSecret, api.QuayOpenShiftCIRepo, api.QuayMirrorFunc, api.QuayTargetNameFunc, nodeArchitectures))
		}
	}

	return append(overridableSteps, buildSteps...), promotionSteps, nil
}

func stepsForImageOverrides(ctx context.Context, overriddenImages map[string]string, consoleHost string, client ctrlruntimeclient.Client, second time.Duration) []api.StepConfiguration {
	var overrideSteps []api.StepConfiguration
	for tag, value := range overriddenImages {
		istRef := api.ImageStreamTagReference{
			Namespace: "ocp",
			Name:      value,
			Tag:       tag,
		}
		inputStep := api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			InputImage: api.InputImage{
				BaseImage: istRef,
				To:        api.PipelineImageStreamTagReference(tag),
			},
		}}
		overrideSteps = append(overrideSteps, inputStep)

		// If we are not running on app.ci we will need to make sure the ImageStreamTag is available
		if !strings.HasSuffix(consoleHost, api.ServiceDomainAPPCI) {
			ensureImageStreamTag(ctx, client, &istRef, second)
		}

		outputStep := api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
			From: api.PipelineImageStreamTagReference(tag),
			To: api.ImageStreamTagReference{
				Name: api.StableImageStream,
				Tag:  tag,
			},
		}}
		overrideSteps = append(overrideSteps, outputStep)
	}
	return overrideSteps
}

// registryDomain determines the domain of the registry we promote to
func registryDomain(configuration *api.PromotionConfiguration) string {
	registry := api.DomainForService(api.ServiceRegistry)
	if configuration.RegistryOverride != "" {
		registry = configuration.RegistryOverride
	}
	return registry
}

// stepForTest creates the appropriate step for each test type.
// Test steps are always leaves and often pruned.  Each one is given its own
// copy of `params` and their values from `Provides` only affect themselves,
// thus avoiding conflicts with other tests pre-pruning.
func stepForTest(
	config *api.ReleaseBuildConfiguration,
	params *api.DeferredParameters,
	podClient kubernetes.PodClient,
	leaseClient *lease.Client,
	templateClient steps.TemplateClient,
	client loggingclient.LoggingClient,
	hiveClient ctrlruntimeclient.WithWatch,
	jobSpec *api.JobSpec,
	inputImages inputImageSet,
	c *api.TestStepConfiguration,
	imageConfigs *[]*api.InputImageTagStepConfiguration,
	pullSecret *coreapi.Secret,
	censor *secrets.DynamicCensor,
	nodeName string,
	targetAdditionalSuffix string,
) ([]api.Step, error) {
	if test := c.MultiStageTestConfigurationLiteral; test != nil {
		leases := api.LeasesForTest(test)
		ipPoolLease := api.IPPoolLeaseForTest(test, config.Metadata)
		if len(leases) != 0 || ipPoolLease.ResourceType != "" {
			params = api.NewDeferredParameters(params)
		}
		var ret []api.Step
		step := multi_stage.MultiStageTestStep(*c, config, params, podClient, jobSpec, leases, nodeName, targetAdditionalSuffix, nil)
		if ipPoolLease.ResourceType != "" {
			step = steps.IPPoolStep(leaseClient, podClient, ipPoolLease, step, params, jobSpec.Namespace)
		}
		if len(leases) != 0 {
			step = steps.LeaseStep(leaseClient, leases, step, jobSpec.Namespace)
		}
		if c.ClusterClaim != nil {
			step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step, censor)
			name := c.ClusterClaim.ClaimRelease(c.As).ReleaseName
			target := api.ReleaseConfiguration{Name: name}.TargetName()
			source := releasesteps.NewReleaseSourceFromClusterClaim(c.As, c.ClusterClaim, hiveClient)
			ret = append(ret, releasesteps.ImportReleaseStep(name, nodeName, target, source, false, config.Resources, podClient, jobSpec, pullSecret, nil))
		}
		addProvidesForStep(step, params)
		ret = append(ret, step)
		ret = append(ret, stepsForStepImages(client, jobSpec, inputImages, test, imageConfigs)...)
		return ret, nil
	}
	if test := c.OpenshiftInstallerClusterTestConfiguration; test != nil {
		if !test.Upgrade {
			return nil, nil
		}
		params = api.NewDeferredParameters(params)
		step, err := clusterinstall.E2ETestStep(*c.OpenshiftInstallerClusterTestConfiguration, *c, params, podClient, templateClient, jobSpec, config.Resources)
		if err != nil {
			return nil, fmt.Errorf("unable to create end to end test step: %w", err)
		}
		step = steps.LeaseStep(leaseClient, []api.StepLease{{
			ResourceType: test.ClusterProfile.LeaseType(),
			Env:          api.DefaultLeaseEnv,
			Count:        1,
		}}, step, jobSpec.Namespace)
		addProvidesForStep(step, params)
		return []api.Step{step}, nil
	}
	step := steps.TestStep(*c, config.Resources, podClient, jobSpec, nodeName)
	if c.ClusterClaim != nil {
		step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step, censor)
	}
	return []api.Step{step}, nil
}

// stepsForStepImages creates steps that import images referenced in test steps.
func stepsForStepImages(
	client loggingclient.LoggingClient,
	jobSpec *api.JobSpec,
	inputImages inputImageSet,
	test *api.MultiStageTestConfigurationLiteral,
	imageConfigs *[]*api.InputImageTagStepConfiguration,
) (ret []api.Step) {
	for _, subStep := range append(append(test.Pre, test.Test...), test.Post...) {
		if link, ok := subStep.FromImageTag(); ok {
			source := api.ImageStreamSource{SourceType: api.ImageStreamSourceTest, Name: subStep.As}

			config := api.InputImageTagStepConfiguration{
				InputImage: api.InputImage{
					BaseImage: *subStep.FromImage,
					To:        link,
				},
				Sources: []api.ImageStreamSource{source},
			}
			// Determine if there are any other steps with the same BaseImage/To.
			if _, ok := inputImages[config.InputImage]; ok {
				for _, existingImageConfig := range *imageConfigs {
					// If the existing step is an image tag step and it has the same image, then add the current step as a
					// source of that same image
					if existingImageConfig.Matches(config.InputImage) {
						existingImageConfig.AddSources(source)
					}
				}
			} else {
				// This image doesn't already exist, so add it.
				inputImages[config.InputImage] = struct{}{}

				step := steps.InputImageTagStep(&config, client, jobSpec)
				ret = append(ret, step)
				*imageConfigs = append(*imageConfigs, &config)
			}
		}
	}
	return
}

// addProvidesForStep adds any required parameters to the deferred parameters map.
// Use this when a step may still need to run even if all parameters are provided
// by the caller as environment variables.
func addProvidesForStep(step api.Step, params *api.DeferredParameters) {
	for name, fn := range step.Provides() {
		params.Add(name, fn)
	}
}

// checkForFullyQualifiedStep if all output parameters of this step are part of the
// environment, replace the step with a shim that automatically provides those variables.
// Returns true if the step was replaced.
func checkForFullyQualifiedStep(step api.Step, params *api.DeferredParameters) (api.Step, bool) {
	provides := step.Provides()

	if values, ok := paramsHasAllParametersAsInput(params, provides); ok {
		step = steps.InputEnvironmentStep(step.Name(), values, step.Creates())
		for k, v := range values {
			params.Set(k, v)
		}
		return step, true
	}
	for name, fn := range provides {
		params.Add(name, fn)
	}
	return step, false
}

// rootImageResolver creates a resolver for the root image import step. We attempt to resolve the root image and
// the build cache. If we are able to successfully determine that the build cache is up-to-date, we import it as
// the root image.
func rootImageResolver(client loggingclient.LoggingClient, ctx context.Context, promote bool) func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
	return func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
		logrus.Debugf("Determining if build cache %s can be used in place of root %s", cache.ISTagName(), root.ISTagName())
		if promote {
			logrus.Debugf("Promotions cannot use the build cache, so using default image %s as root image.", root.ISTagName())
			return root, nil
		}
		cacheTag := &imagev1.ImageStreamTag{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: cache.Namespace, Name: fmt.Sprintf("%s:%s", cache.Name, cache.Tag)}, cacheTag); err != nil {
			if kapierrors.IsNotFound(err) {
				logrus.Debugf("Build cache %s not found, falling back to %s", cache.ISTagName(), root.ISTagName())
				// no build cache, use the normal root
				return root, nil
			}
			return nil, fmt.Errorf("could not resolve build cache image stream tag %s: %w", cache.ISTagName(), err)
		}

		// If the image contains a manifest list, the docker metadata are empty. Instead
		// we need to grab the metadata from one of the images in manifest list.
		if len(cacheTag.Image.DockerImageManifests) > 0 {
			imageDigest := cacheTag.Image.DockerImageManifests[0].Digest

			img := &imagev1.Image{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: imageDigest}, img); err != nil {
				return nil, fmt.Errorf("could not fetch image %s: %w", imageDigest, err)
			}

			cacheTag.Image = *img
		}

		logrus.Debugf("Resolved build cache %s to %s", cache.ISTagName(), cacheTag.Image.Name)
		metadata := &docker10.DockerImage{}
		if len(cacheTag.Image.DockerImageMetadata.Raw) == 0 {
			return nil, fmt.Errorf("could not fetch Docker image metadata build cache %s", cache.ISTagName())
		}
		if err := json.Unmarshal(cacheTag.Image.DockerImageMetadata.Raw, metadata); err != nil {
			return nil, fmt.Errorf("malformed Docker image metadata on build cache %s: %w", cache.ISTagName(), err)
		}
		prior := metadata.Config.Labels[api.ImageVersionLabel(api.PipelineImageStreamTagReferenceRoot)]
		logrus.Debugf("Build cache %s is based on root image at %s", cache.ISTagName(), prior)

		rootTag := &imagev1.ImageStreamTag{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: root.Namespace, Name: fmt.Sprintf("%s:%s", root.Name, root.Tag)}, rootTag); err != nil {
			return nil, fmt.Errorf("could not resolve build root image stream tag %s: %w", root.ISTagName(), err)
		}
		logrus.Debugf("Resolved root image %s to %s", root.ISTagName(), rootTag.Image.Name)
		current := rootTag.Image.Name
		if prior == current {
			logrus.Debugf("Using build cache %s as root image.", cache.ISTagName())
			return cache, nil
		}
		logrus.Debugf("Using default image %s as root image.", root.ISTagName())
		return root, nil
	}
}

type readFile func(string) ([]byte, error)
type resolveRoot func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error)

// FromConfigStatic pre-parses the configuration into step graph configuration.
// This graph configuration can then be used to perform validation and build the
// final execution graph.  See also FromConfig.
func FromConfigStatic(config *api.ReleaseBuildConfiguration) api.GraphConfiguration {
	var buildSteps []api.StepConfiguration

	buildRoots := config.InputConfiguration.BuildRootImages
	if buildRoots == nil {
		buildRoots = make(map[string]api.BuildRootImageConfiguration)
	}
	if target := config.InputConfiguration.BuildRootImage; target != nil {
		buildRoots[""] = *target
	}

	for repo, target := range buildRoots {
		root := string(api.PipelineImageStreamTagReferenceRoot)
		if repo != "" {
			root = fmt.Sprintf("%s-%s", root, repo)
		}
		if target.FromRepository {
			config := api.InputImageTagStepConfiguration{
				InputImage: api.InputImage{
					To:  api.PipelineImageStreamTagReference(root),
					Ref: repo,
				},
				Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceType(root)}},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				InputImageTagStepConfiguration: &config,
			})
		} else if isTagRef := target.ImageStreamTagReference; isTagRef != nil {
			config := api.InputImageTagStepConfiguration{
				InputImage: api.InputImage{
					BaseImage: *isTagRef,
					To:        api.PipelineImageStreamTagReference(root),
					Ref:       repo,
				},
				Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceType(root)}},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				InputImageTagStepConfiguration: &config,
			})
		} else if gitSourceRef := target.ProjectImageBuild; gitSourceRef != nil {
			if repo != "" {
				gitSourceRef.Ref = repo
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				ProjectDirectoryImageBuildInputs: gitSourceRef,
			})
		}
	}

	binaryBuildCommandsList := config.BinaryBuildCommandsList
	if len(config.BinaryBuildCommands) > 0 {
		binaryBuildCommandsList = append(binaryBuildCommandsList, api.RefCommands{Commands: config.BinaryBuildCommands})
	}
	for _, binaryBuildCommand := range binaryBuildCommandsList {
		bin := string(api.PipelineImageStreamTagReferenceBinaries)
		src := string(api.PipelineImageStreamTagReferenceSource)
		if binaryBuildCommand.Ref != "" {
			bin = fmt.Sprintf("%s-%s", bin, binaryBuildCommand.Ref)
			src = fmt.Sprintf("%s-%s", src, binaryBuildCommand.Ref)
		}
		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     api.PipelineImageStreamTagReference(src),
			To:       api.PipelineImageStreamTagReference(bin),
			Commands: binaryBuildCommand.Commands,
			Ref:      binaryBuildCommand.Ref,
		}})
	}

	testBinaryBuildCommandsList := config.TestBinaryBuildCommandsList
	if len(config.TestBinaryBuildCommands) > 0 {
		testBinaryBuildCommandsList = append(testBinaryBuildCommandsList, api.RefCommands{Commands: config.TestBinaryBuildCommands})
	}
	for _, testBinaryBuildCommands := range testBinaryBuildCommandsList {
		testBin := string(api.PipelineImageStreamTagReferenceTestBinaries)
		src := string(api.PipelineImageStreamTagReferenceSource)
		if testBinaryBuildCommands.Ref != "" {
			testBin = fmt.Sprintf("%s-%s", testBin, testBinaryBuildCommands.Ref)
			src = fmt.Sprintf("%s-%s", src, testBinaryBuildCommands.Ref)
		}
		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     api.PipelineImageStreamTagReference(src),
			To:       api.PipelineImageStreamTagReference(testBin),
			Commands: testBinaryBuildCommands.Commands,
			Ref:      testBinaryBuildCommands.Ref,
		}})
	}

	rpmBuildCommandsList := config.RpmBuildCommandsList
	if len(config.RpmBuildCommands) > 0 {
		rpmBuildCommandsList = append(rpmBuildCommandsList, api.RefCommands{Commands: config.RpmBuildCommands})
	}
	rpmBuildLocationList := config.RpmBuildLocationList
	if len(config.RpmBuildLocation) > 0 {
		rpmBuildLocationList = append(rpmBuildLocationList, api.RefLocation{Location: config.RpmBuildLocation})
	}

	for _, rpmBuildCommands := range rpmBuildCommandsList {

		var matchingBinaryBuildCommands string
		for _, binaryBuildCommands := range binaryBuildCommandsList {
			if rpmBuildCommands.Ref == binaryBuildCommands.Ref {
				matchingBinaryBuildCommands = binaryBuildCommands.Commands
			}
		}
		from := string(api.PipelineImageStreamTagReferenceSource)
		if len(matchingBinaryBuildCommands) > 0 {
			from = string(api.PipelineImageStreamTagReferenceBinaries)
		}
		if rpmBuildCommands.Ref != "" {
			from = fmt.Sprintf("%s-%s", from, rpmBuildCommands.Ref)
		}

		var matchingRpmBuildLocation string
		for _, rpmBuildLocation := range rpmBuildLocationList {
			if rpmBuildCommands.Ref == rpmBuildLocation.Ref {
				matchingRpmBuildLocation = rpmBuildLocation.Location
			}
		}
		var out string
		if matchingRpmBuildLocation != "" {
			out = matchingRpmBuildLocation
		} else {
			out = api.DefaultRPMLocation
		}
		rpms := string(api.PipelineImageStreamTagReferenceRPMs)
		if rpmBuildCommands.Ref != "" {
			rpms = fmt.Sprintf("%s-%s", rpms, rpmBuildCommands.Ref)
		}

		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     api.PipelineImageStreamTagReference(from),
			To:       api.PipelineImageStreamTagReference(rpms),
			Commands: fmt.Sprintf(`%s; ln -s $( pwd )/%s %s`, rpmBuildCommands.Commands, out, api.RPMServeLocation),
			Ref:      rpmBuildCommands.Ref,
		}})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
			From: api.PipelineImageStreamTagReference(rpms),
			Ref:  rpmBuildCommands.Ref,
		}})
	}

	for alias, baseImage := range config.BaseImages {
		config := api.InputImageTagStepConfiguration{
			InputImage: api.InputImage{
				BaseImage: defaultImageFromReleaseTag(alias, baseImage, config.ReleaseTagConfiguration),
				To:        api.PipelineImageStreamTagReference(alias),
			},
			Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceBase, Name: alias}},
		}
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &config})

	}

	for alias, target := range config.InputConfiguration.BaseRPMImages {
		intermediateTag := api.PipelineImageStreamTagReference(fmt.Sprintf("%s-without-rpms", alias))
		config := api.InputImageTagStepConfiguration{
			InputImage: api.InputImage{
				BaseImage: defaultImageFromReleaseTag(alias, target, config.ReleaseTagConfiguration),
				To:        intermediateTag,
			},
			Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceBaseRpm, Name: alias}},
		}
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &config})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
			From: intermediateTag,
			To:   api.PipelineImageStreamTagReference(alias),
		}})
	}

	for i := range config.Images {
		image := &config.Images[i]
		stableImageTag := string(image.To)
		if image.Ref != "" {
			stableImageTag = strings.TrimSuffix(stableImageTag, fmt.Sprintf("-%s", image.Ref))
		}
		buildSteps = append(buildSteps,
			api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image},
			api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
				From: image.To,
				To: api.ImageStreamTagReference{
					Name: api.StableImageStream,
					Tag:  stableImageTag,
				},
				Optional: image.Optional,
			}})
	}

	if config.Operator != nil {
		// Build a bundle source image that substitutes all values in `substitutions` in all `manifests` directories
		buildSteps = append(buildSteps, api.StepConfiguration{BundleSourceStepConfiguration: &api.BundleSourceStepConfiguration{
			Substitutions: config.Operator.Substitutions,
		}})
		// Build bundles
		// First build named bundles and corresponding indices
		// store list of indices for unnamed bundles
		var unnamedBundles []int
		for index, bundleConfig := range config.Operator.Bundles {
			if bundleConfig.As == "" {
				unnamedBundles = append(unnamedBundles, index)
				continue
			}
			bundle := &api.ProjectDirectoryImageBuildStepConfiguration{
				To: api.PipelineImageStreamTagReference(bundleConfig.As),
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					ContextDir:     bundleConfig.ContextDir,
					DockerfilePath: bundleConfig.DockerfilePath,
				},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: bundle})
			// Build index generator
			indexName := api.PipelineImageStreamTagReference(api.IndexName(bundleConfig.As))
			updateGraph := bundleConfig.UpdateGraph
			if updateGraph == "" {
				updateGraph = api.IndexUpdateSemver
			}
			buildSteps = append(buildSteps, api.StepConfiguration{IndexGeneratorStepConfiguration: &api.IndexGeneratorStepConfiguration{
				To:            api.IndexGeneratorName(indexName),
				OperatorIndex: []string{bundleConfig.As},
				BaseIndex:     bundleConfig.BaseIndex,
				UpdateGraph:   updateGraph,
			}})
			// Build the index
			index := &api.ProjectDirectoryImageBuildStepConfiguration{
				To: indexName,
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					DockerfilePath: steps.IndexDockerfileName,
				},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: index})
		}
		// Build non-named bundles following old naming system
		var bundles []string
		for _, bundleIndex := range unnamedBundles {
			bundle := config.Operator.Bundles[bundleIndex]
			bundleName := api.BundleName(bundleIndex)
			bundles = append(bundles, bundleName)
			image := &api.ProjectDirectoryImageBuildStepConfiguration{
				To: api.PipelineImageStreamTagReference(bundleName),
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					ContextDir:     bundle.ContextDir,
					DockerfilePath: bundle.DockerfilePath,
				},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image})
		}
		if len(bundles) > 0 {
			// Build index generator
			buildSteps = append(buildSteps, api.StepConfiguration{IndexGeneratorStepConfiguration: &api.IndexGeneratorStepConfiguration{
				To:            api.PipelineImageStreamTagReferenceIndexImageGenerator,
				OperatorIndex: bundles,
				UpdateGraph:   api.IndexUpdateSemver,
			}})
			// Build the index
			image := &api.ProjectDirectoryImageBuildStepConfiguration{
				To: api.PipelineImageStreamTagReferenceIndexImage,
				ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
					DockerfilePath: steps.IndexDockerfileName,
				},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image})
		}
	}

	for i := range config.Tests {
		test := &config.Tests[i]
		if test.ContainerTestConfiguration != nil || test.MultiStageTestConfigurationLiteral != nil || (test.OpenshiftInstallerClusterTestConfiguration != nil && test.OpenshiftInstallerClusterTestConfiguration.Upgrade) {
			if test.Secret != nil {
				test.Secrets = append(test.Secrets, test.Secret)
			}
			if test.ContainerTestConfiguration != nil && test.ContainerTestConfiguration.Clone == nil {
				test.ContainerTestConfiguration.Clone = utilpointer.Bool(config.IsBaseImage(string(test.ContainerTestConfiguration.From)))
			}
			buildSteps = append(buildSteps, api.StepConfiguration{TestStepConfiguration: test})
		}
	}

	if config.ReleaseTagConfiguration != nil {
		buildSteps = append(buildSteps, api.StepConfiguration{ReleaseImagesTagStepConfiguration: config.ReleaseTagConfiguration})
	}
	for name := range config.Releases {
		buildSteps = append(buildSteps, api.StepConfiguration{ResolvedReleaseImagesStepConfiguration: &api.ReleaseConfiguration{
			Name:              name,
			UnresolvedRelease: config.Releases[name],
		}})
	}

	buildSteps = append(buildSteps, config.RawSteps...)
	return api.GraphConfiguration{Steps: buildSteps}
}

const codeMountPath = "/home/prow/go"

func runtimeStepConfigsForBuild(
	config *api.ReleaseBuildConfiguration,
	jobSpec *api.JobSpec,
	readFile readFile,
	resolveRoot resolveRoot,
	imageConfigs []*api.InputImageTagStepConfiguration,
) ([]api.StepConfiguration, error) {
	buildRoots := config.InputConfiguration.BuildRootImages
	if buildRoots == nil {
		buildRoots = make(map[string]api.BuildRootImageConfiguration)
	}
	if target := config.InputConfiguration.BuildRootImage; target != nil { //This will only be the case when config.InputConfiguration.BuildRootImages is empty
		buildRoots[""] = *target
	}
	refs := jobSpec.ExtraRefs
	if jobSpec.Refs != nil {
		refs = append(refs, *jobSpec.Refs)
	}
	var buildSteps []api.StepConfiguration
	for ref, root := range buildRoots {
		rootTag := string(api.PipelineImageStreamTagReferenceRoot)
		if ref != "" {
			rootTag = fmt.Sprintf("%s-%s", rootTag, ref)
		}
		var target *api.InputImageTagStepConfiguration
		if root.FromRepository || root.UseBuildCache {
			for i, s := range imageConfigs {
				if s.InputImage.To == api.PipelineImageStreamTagReference(rootTag) {
					target = imageConfigs[i]
					break
				}
			}
		}
		if target != nil {
			istTagRef := &target.InputImage.BaseImage
			if root.FromRepository {
				path := "."        // By default, the path will be the working directory
				if len(refs) > 1 { // If we are getting the build root image for a specific ref we must determine the absolute path
					var matchingRefs []prowapi.Refs
					for _, r := range refs {
						if ref == fmt.Sprintf("%s.%s", r.Org, r.Repo) {
							matchingRefs = append(matchingRefs, r)
						}
					}
					if len(matchingRefs) == 0 { // If we didn't find anything, use the primary refs
						matchingRefs = append(matchingRefs, *jobSpec.Refs)
					}
					path = decorate.DetermineWorkDir(codeMountPath, matchingRefs)
				}
				var err error
				istTagRef, err = buildRootImageStreamFromRepository(path, readFile)
				if err != nil {
					return nil, fmt.Errorf("failed to read buildRootImageStream from repository: %w", err)
				}
			}
			if root.UseBuildCache {
				metadata := config.Metadata
				if ref != "" {
					orgRepo := strings.Split(ref, ".")
					org, repo, branch := orgRepo[0], orgRepo[1], ""
					for _, jobSpecRef := range jobSpec.ExtraRefs {
						if jobSpecRef.Org == org && jobSpecRef.Repo == repo {
							branch = jobSpecRef.BaseRef
							break
						}
					}
					metadata = api.Metadata{
						Org:    org,
						Repo:   repo,
						Branch: branch,
					}
				}
				cache := api.BuildCacheFor(metadata)
				root, err := resolveRoot(istTagRef, &cache)
				if err != nil {
					return nil, fmt.Errorf("could not resolve build root: %w", err)
				}
				istTagRef = root
			}
			target.InputImage.BaseImage = *istTagRef
		}
	}

	if jobSpec.Refs != nil {
		buildSteps = append(buildSteps, sourceStepForRef(jobSpec.Refs, true))
	}
	for _, ref := range jobSpec.ExtraRefs {
		buildSteps = append(buildSteps, sourceStepForRef(&ref, false))
	}
	return buildSteps, nil
}

func sourceStepForRef(ref *prowapi.Refs, primaryRef bool) api.StepConfiguration {
	orgRepo := ""
	root := api.PipelineImageStreamTagReferenceRoot
	source := api.PipelineImageStreamTagReferenceSource
	if !primaryRef { // We only care about these suffixes when building extra refs
		orgRepo = fmt.Sprintf("%s.%s", ref.Org, ref.Repo)
		root = api.PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", api.PipelineImageStreamTagReferenceRoot, orgRepo))
		source = api.PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", api.PipelineImageStreamTagReferenceSource, orgRepo))
	}
	return api.StepConfiguration{SourceStepConfiguration: &api.SourceStepConfiguration{
		From: root,
		To:   source,
		ClonerefsImage: api.ImageStreamTagReference{
			Namespace: "ci",
			Name:      "managed-clonerefs",
			Tag:       "latest",
		},
		ClonerefsPath: "/clonerefs",
		Ref:           orgRepo,
	}}
}

func paramsHasAllParametersAsInput(p api.Parameters, params map[string]func() (string, error)) (map[string]string, bool) {
	if len(params) == 0 {
		return nil, false
	}
	var values map[string]string
	for k := range params {
		if !p.HasInput(k) {
			return nil, false
		}
		if values == nil {
			values = make(map[string]string)
		}
		v, err := p.Get(k)
		if err != nil {
			return nil, false
		}
		values[k] = v
	}
	return values, true
}

func defaultImageFromReleaseTag(alias string, base api.ImageStreamTagReference, release *api.ReleaseTagConfiguration) api.ImageStreamTagReference {
	// ensure the "As" field is set to the provided alias.
	base.As = alias
	if release == nil {
		return base
	}
	if len(base.Tag) == 0 || len(base.Name) > 0 || len(base.Namespace) > 0 {
		return base
	}
	base.Name = release.Name
	base.Namespace = release.Namespace
	return base
}

// validateCIOperatorInrepoConfig validates the content of the in-repo .ci-operator.yaml file
// These need to be validated separately because they are dynamically loaded by ci-operator at runtime and are part
// of the tested repository, not static configuration validated by Validator.
func validateCIOperatorInrepoConfig(inrepoConfig *api.CIOperatorInrepoConfig) error {
	root := inrepoConfig.BuildRootImage
	if root.Namespace == "" || root.Name == "" || root.Tag == "" {
		return fmt.Errorf("invalid .ci-operator.yaml: all build_root_image members (namespace, name, tag) must be non-empty")
	}
	return nil
}

func buildRootImageStreamFromRepository(path string, readFile readFile) (*api.ImageStreamTagReference, error) {
	filePath := fmt.Sprintf("%s/%s", path, api.CIOperatorInrepoConfigFileName)
	data, err := readFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s file: %w", api.CIOperatorInrepoConfigFileName, err)
	}
	config := api.CIOperatorInrepoConfig{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", api.CIOperatorInrepoConfigFileName, err)
	}

	return &config.BuildRootImage, validateCIOperatorInrepoConfig(&config)
}

func ensureImageStreamTag(ctx context.Context, client ctrlruntimeclient.Client, isTagRef *api.ImageStreamTagReference, second time.Duration) {
	istImport := &testimagestreamtagimportv1.TestImageStreamTagImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isTagRef.Name + "-" + isTagRef.Tag,
			Namespace: "ci",
			Labels:    map[string]string{api.DPTPRequesterLabel: "ci-operator"},
		},
		Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
			Namespace: isTagRef.Namespace,
			Name:      isTagRef.Name + ":" + isTagRef.Tag,
		},
	}
	istImport.WithImageStreamLabels()

	// Conflicts are expected
	if err := client.Create(ctx, istImport); err != nil && !kapierrors.IsAlreadyExists(err) {
		logrus.WithError(err).Warnf("Failed to create imagestreamtagimport for root %s", isTagRef.ISTagName())
	}

	name := types.NamespacedName{Namespace: isTagRef.Namespace, Name: isTagRef.Name + ":" + isTagRef.Tag}
	if err := wait.PollImmediate(5*second, 30*second, func() (bool, error) {
		if err := client.Get(ctx, name, &imagev1.ImageStreamTag{}); err != nil {
			if kapierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("get failed: %w", err)
		}
		return true, nil
	}); err != nil {
		logrus.WithError(err).Warnf("Waiting for imagestreamtag %s failed", isTagRef.ISTagName())
	}
}

func resolveCLIOverrideImage(architecture api.ReleaseArchitecture, version string) (*coreapi.ObjectReference, error) {
	if architecture == "" || architecture == api.ReleaseArchitectureAMD64 {
		return nil, nil
	}
	if version == "" {
		return nil, errors.New("non-amd64 releases require a version to be configured")
	}
	majorMinor, err := official.ExtractMajorMinor(version)
	if err != nil {
		return nil, err
	}

	isTagRef := api.ImageStreamTagReference{
		Namespace: "ocp",
		Name:      majorMinor,
		Tag:       "cli",
	}

	return &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(isTagRef)}, nil
}
