package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
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
	"sigs.k8s.io/yaml"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	templateapi "github.com/openshift/api/template/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
	"github.com/openshift/ci-tools/pkg/release/official"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/clusterinstall"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
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
	leaseClient *lease.Client,
	requiredTargets []string,
	cloneAuthConfig *steps.CloneAuthConfig,
	pullSecret, pushSecret *coreapi.Secret,
	censor *secrets.DynamicCensor,
	hiveKubeconfig *rest.Config,
	consoleHost string,
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
	buildClient := steps.NewBuildClient(client, buildGetter.RESTClient())

	templateGetter, err := templateclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get template client for cluster config: %w", err)
	}
	templateClient := steps.NewTemplateClient(client, templateGetter.RESTClient())

	coreGetter, err := coreclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get core client for cluster config: %w", err)
	}

	podClient := steps.NewPodClient(client, clusterConfig, coreGetter.RESTClient())

	var hiveClient ctrlruntimeclient.WithWatch
	if hiveKubeconfig != nil {
		hiveClient, err = ctrlruntimeclient.NewWithWatch(hiveKubeconfig, ctrlruntimeclient.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("could not get Hive client for Hive kube config: %w", err)
		}
	}
	httpClient := retryablehttp.NewClient()
	httpClient.Logger = nil

	return fromConfig(ctx, config, graphConf, jobSpec, templates, paramFile, promote, client, buildClient, templateClient, podClient, leaseClient, hiveClient, httpClient.StandardClient(), requiredTargets, cloneAuthConfig, pullSecret, pushSecret, api.NewDeferredParameters(nil), censor, consoleHost)
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
	podClient steps.PodClient,
	leaseClient *lease.Client,
	hiveClient ctrlruntimeclient.WithWatch,
	httpClient release.HTTPClient,
	requiredTargets []string,
	cloneAuthConfig *steps.CloneAuthConfig,
	pullSecret, pushSecret *coreapi.Secret,
	params *api.DeferredParameters,
	censor *secrets.DynamicCensor,
	consoleHost string,
) ([]api.Step, []api.Step, error) {
	requiredNames := sets.NewString()
	for _, target := range requiredTargets {
		requiredNames.Insert(target)
	}
	params.Add("JOB_NAME", func() (string, error) { return jobSpec.Job, nil })
	params.Add("JOB_NAME_HASH", func() (string, error) { return jobSpec.JobNameHash(), nil })
	params.Add("JOB_NAME_SAFE", func() (string, error) { return strings.Replace(jobSpec.Job, "_", "-", -1), nil })
	params.Add("NAMESPACE", func() (string, error) { return jobSpec.Namespace(), nil })
	inputImages := make(inputImageSet)
	var overridableSteps, buildSteps, postSteps []api.Step
	var imageStepLinks []api.StepLink
	var hasReleaseStep bool
	resolver := rootImageResolver(client, ctx, promote)
	imageConfigs := graphConf.InputImages()
	rawSteps, err := runtimeStepConfigsForBuild(ctx, client, config, jobSpec, ioutil.ReadFile, resolver, imageConfigs, time.Second, consoleHost)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get steps from configuration: %w", err)
	}
	rawSteps = append(graphConf.Steps, rawSteps...)
	for _, rawStep := range rawSteps {
		if testStep := rawStep.TestStepConfiguration; testStep != nil {
			steps, testHasReleaseStep, err := stepForTest(ctx, config, params, podClient, leaseClient, templateClient, client, hiveClient, jobSpec, inputImages, testStep, &imageConfigs, pullSecret, censor)
			if err != nil {
				return nil, nil, err
			}
			if testHasReleaseStep {
				hasReleaseStep = true
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
			if env := utils.ReleaseImageEnv(resolveConfig.Name); params.HasInput(env) {
				value, err = params.Get(env)
				if err != nil {
					return nil, nil, results.ForReason("resolving_release").ForError(fmt.Errorf("failed to get %q parameter: %w", env, err))
				}
				logrus.Infof("Using explicitly provided pull-spec for release %s (%s)", resolveConfig.Name, value)
			} else {
				switch {
				case resolveConfig.Integration != nil:
					logrus.Infof("Building release %s from a snapshot of %s/%s", resolveConfig.Name, resolveConfig.Integration.Namespace, resolveConfig.Integration.Name)
					// this is the one case where we're not importing a payload, we need to get the images and build one
					snapshot := releasesteps.ReleaseSnapshotStep(resolveConfig.Name, *resolveConfig.Integration, podClient, jobSpec)
					assemble := releasesteps.AssembleReleaseStep(resolveConfig.Name, &api.ReleaseTagConfiguration{
						Namespace:          resolveConfig.Integration.Namespace,
						Name:               resolveConfig.Integration.Name,
						IncludeBuiltImages: resolveConfig.Integration.IncludeBuiltImages,
					}, config.Resources, podClient, jobSpec)
					for _, s := range []api.Step{snapshot, assemble} {
						buildSteps = append(buildSteps, s)
						addProvidesForStep(s, params)
					}
					imageStepLinks = append(imageStepLinks, snapshot.Creates()...)
				case resolveConfig.Candidate != nil:
					value, err = candidate.ResolvePullSpec(httpClient, *resolveConfig.Candidate)
				case resolveConfig.Release != nil:
					value, _, err = official.ResolvePullSpecAndVersion(httpClient, *resolveConfig.Release)
				case resolveConfig.Prerelease != nil:
					value, err = prerelease.ResolvePullSpec(httpClient, *resolveConfig.Prerelease)
				}
				if err != nil {
					return nil, nil, results.ForReason("resolving_release").ForError(fmt.Errorf("failed to resolve release %s: %w", resolveConfig.Name, err))
				}
			}
			if value != "" {
				logrus.Infof("Resolved release %s to %s", resolveConfig.Name, value)
				step := releasesteps.ImportReleaseStep(resolveConfig.Name, resolveConfig.TargetName(), value, false, config.Resources, podClient, jobSpec, pullSecret, overrideCLIReleaseExtractImage)
				buildSteps = append(buildSteps, step)
				addProvidesForStep(step, params)
			}
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
			step = steps.PipelineImageCacheStep(*rawStep.PipelineImageCacheStepConfiguration, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.SourceStepConfiguration != nil {
			step = steps.SourceStep(*rawStep.SourceStepConfiguration, config.Resources, buildClient, jobSpec, cloneAuthConfig, pullSecret)
		} else if rawStep.BundleSourceStepConfiguration != nil {
			step = steps.BundleSourceStep(*rawStep.BundleSourceStepConfiguration, config, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.IndexGeneratorStepConfiguration != nil {
			step = steps.IndexGeneratorStep(*rawStep.IndexGeneratorStepConfiguration, config, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
			step = steps.ProjectDirectoryImageBuildStep(*rawStep.ProjectDirectoryImageBuildStepConfiguration, config, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.ProjectDirectoryImageBuildInputs != nil {
			step = steps.GitSourceStep(*rawStep.ProjectDirectoryImageBuildInputs, config.Resources, buildClient, jobSpec, cloneAuthConfig, pullSecret)
		} else if rawStep.RPMImageInjectionStepConfiguration != nil {
			step = steps.RPMImageInjectionStep(*rawStep.RPMImageInjectionStepConfiguration, config.Resources, buildClient, jobSpec, pullSecret)
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
			step = releasesteps.ReleaseImagesTagStep(*rawStep.ReleaseImagesTagStepConfiguration, client, params, jobSpec)
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
					logrus.Infof("Resolved release %s to %s", name, pullSpec)
					target := rawStep.ReleaseImagesTagStepConfiguration.TargetName(name)
					releaseStep = releasesteps.ImportReleaseStep(name, target, pullSpec, true, config.Resources, podClient, jobSpec, pullSecret, nil)
				} else {
					// for backwards compatibility, users get inclusion for free with tag_spec
					cfg := *rawStep.ReleaseImagesTagStepConfiguration
					cfg.IncludeBuiltImages = name == api.LatestReleaseName
					releaseStep = releasesteps.AssembleReleaseStep(name, &cfg, config.Resources, podClient, jobSpec)
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

	if promote {
		if pushSecret == nil {
			return nil, nil, errors.New("--image-mirror-push-secret is required for promoting images")
		}
		if config.PromotionConfiguration == nil {
			return nil, nil, fmt.Errorf("cannot promote images, no promotion configuration defined")
		}
		postSteps = append(postSteps, releasesteps.PromotionStep(config, requiredNames, jobSpec, podClient, pushSecret))
	}

	return append(overridableSteps, buildSteps...), postSteps, nil
}

// stepForTest creates the appropriate step for each test type.
// Test steps are always leaves and often pruned.  Each one is given its own
// copy of `params` and their values from `Provides` only affect themselves,
// thus avoiding conflicts with other tests pre-pruning.
func stepForTest(
	ctx context.Context,
	config *api.ReleaseBuildConfiguration,
	params *api.DeferredParameters,
	podClient steps.PodClient,
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
) ([]api.Step, bool, error) {
	var hasReleaseStep bool
	if test := c.MultiStageTestConfigurationLiteral; test != nil {
		leases := api.LeasesForTest(test)
		if len(leases) != 0 {
			params = api.NewDeferredParameters(params)
		}
		var testSteps []api.Step
		step := steps.MultiStageTestStep(*c, config, params, podClient, jobSpec, leases)
		if len(leases) != 0 {
			step = steps.LeaseStep(leaseClient, leases, step, jobSpec.Namespace)
			addProvidesForStep(step, params)
		}
		// hive client may not be present for jobs that execute non-claim based tests
		if hiveClient != nil && c.ClusterClaim != nil {
			step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step, censor)
			pullSpec, err := getClusterPoolPullSpec(ctx, c.ClusterClaim, hiveClient)
			if err != nil {
				return nil, hasReleaseStep, err
			}
			hasReleaseStep = true
			claimRelease := c.ClusterClaim.ClaimRelease(c.As)
			logrus.Infof("Resolved release %s to %s", claimRelease.ReleaseName, pullSpec)
			target := api.ReleaseConfiguration{Name: claimRelease.ReleaseName}.TargetName()
			importStep := releasesteps.ImportReleaseStep(claimRelease.ReleaseName, target, pullSpec, false, config.Resources, podClient, jobSpec, pullSecret, nil)
			testSteps = append(testSteps, importStep)
			addProvidesForStep(step, params)
		}
		testSteps = append(testSteps, step)
		newSteps := stepsForStepImages(client, jobSpec, inputImages, test, imageConfigs)
		return append(testSteps, newSteps...), hasReleaseStep, nil
	}
	if test := c.OpenshiftInstallerClusterTestConfiguration; test != nil {
		if !test.Upgrade {
			return nil, hasReleaseStep, nil
		}
		params = api.NewDeferredParameters(params)
		step, err := clusterinstall.E2ETestStep(*c.OpenshiftInstallerClusterTestConfiguration, *c, params, podClient, templateClient, jobSpec, config.Resources)
		if err != nil {
			return nil, hasReleaseStep, fmt.Errorf("unable to create end to end test step: %w", err)
		}
		step = steps.LeaseStep(leaseClient, []api.StepLease{{
			ResourceType: test.ClusterProfile.LeaseType(),
			Env:          api.DefaultLeaseEnv,
			Count:        1,
		}}, step, jobSpec.Namespace)
		addProvidesForStep(step, params)
		return []api.Step{step}, hasReleaseStep, nil
	}
	step := steps.TestStep(*c, config.Resources, podClient, jobSpec)
	if c.ClusterClaim != nil {
		step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step, censor)
	}
	return []api.Step{step}, hasReleaseStep, nil
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
	if target := config.InputConfiguration.BuildRootImage; target != nil {
		if target.FromRepository {
			config := api.InputImageTagStepConfiguration{
				InputImage: api.InputImage{
					To: api.PipelineImageStreamTagReferenceRoot,
				},
				Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				InputImageTagStepConfiguration: &config,
			})
		} else if isTagRef := target.ImageStreamTagReference; isTagRef != nil {
			config := api.InputImageTagStepConfiguration{
				InputImage: api.InputImage{
					BaseImage: *isTagRef,
					To:        api.PipelineImageStreamTagReferenceRoot,
				},
				Sources: []api.ImageStreamSource{{SourceType: api.ImageStreamSourceRoot}},
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				InputImageTagStepConfiguration: &config,
			})
		} else if gitSourceRef := target.ProjectImageBuild; gitSourceRef != nil {
			buildSteps = append(buildSteps, api.StepConfiguration{
				ProjectDirectoryImageBuildInputs: gitSourceRef,
			})
		}
	}
	if len(config.BinaryBuildCommands) > 0 {
		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     api.PipelineImageStreamTagReferenceSource,
			To:       api.PipelineImageStreamTagReferenceBinaries,
			Commands: config.BinaryBuildCommands,
		}})
	}

	if len(config.TestBinaryBuildCommands) > 0 {
		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     api.PipelineImageStreamTagReferenceSource,
			To:       api.PipelineImageStreamTagReferenceTestBinaries,
			Commands: config.TestBinaryBuildCommands,
		}})
	}

	if len(config.RpmBuildCommands) > 0 {
		var from api.PipelineImageStreamTagReference
		if len(config.BinaryBuildCommands) > 0 {
			from = api.PipelineImageStreamTagReferenceBinaries
		} else {
			from = api.PipelineImageStreamTagReferenceSource
		}

		var out string
		if config.RpmBuildLocation != "" {
			out = config.RpmBuildLocation
		} else {
			out = api.DefaultRPMLocation
		}

		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     from,
			To:       api.PipelineImageStreamTagReferenceRPMs,
			Commands: fmt.Sprintf(`%s; ln -s $( pwd )/%s %s`, config.RpmBuildCommands, out, api.RPMServeLocation),
		}})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
			From: api.PipelineImageStreamTagReferenceRPMs,
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
		buildSteps = append(buildSteps,
			api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image},
			api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
				From: image.To,
				To: api.ImageStreamTagReference{
					Name: api.StableImageStream,
					Tag:  string(image.To),
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
				test.ContainerTestConfiguration.Clone = utilpointer.BoolPtr(config.IsBaseImage(string(test.ContainerTestConfiguration.From)))
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

func runtimeStepConfigsForBuild(
	ctx context.Context,
	client ctrlruntimeclient.Client,
	config *api.ReleaseBuildConfiguration,
	jobSpec *api.JobSpec,
	readFile readFile,
	resolveRoot resolveRoot,
	imageConfigs []*api.InputImageTagStepConfiguration,
	second time.Duration,
	consoleHost string,
) ([]api.StepConfiguration, error) {
	var buildSteps []api.StepConfiguration
	if root := config.InputConfiguration.BuildRootImage; root != nil {
		var target *api.InputImageTagStepConfiguration
		if root.FromRepository || root.UseBuildCache {
			for i, s := range imageConfigs {
				if to := s.InputImage.To; to == api.PipelineImageStreamTagReferenceRoot {
					target = imageConfigs[i]
					break
				}
			}
		}
		if target != nil {
			istTagRef := &target.InputImage.BaseImage
			if root.FromRepository {
				var err error
				istTagRef, err = buildRootImageStreamFromRepository(readFile)
				if err != nil {
					return nil, fmt.Errorf("failed to read buildRootImageStream from repository: %w", err)
				}
			}
			if root.UseBuildCache {
				cache := api.BuildCacheFor(config.Metadata)
				root, err := resolveRoot(istTagRef, &cache)
				if err != nil {
					return nil, fmt.Errorf("could not resolve build root: %w", err)
				}
				istTagRef = root
			}
			// if ci-operator runs on app.ci, we do not need to import the image because
			// the istTagRef has to be an image stream tag on app.ci
			if root.FromRepository && !strings.HasSuffix(consoleHost, api.ServiceDomainAPPCI) {
				ensureImageStreamTag(ctx, client, istTagRef, second)
			}
			target.InputImage.BaseImage = *istTagRef
		}
	}
	if jobSpec.Refs != nil || len(jobSpec.ExtraRefs) > 0 {
		step := api.StepConfiguration{SourceStepConfiguration: &api.SourceStepConfiguration{
			From: api.PipelineImageStreamTagReferenceRoot,
			To:   api.PipelineImageStreamTagReferenceSource,
			ClonerefsImage: api.ImageStreamTagReference{
				Namespace: "ci",
				Name:      "managed-clonerefs",
				Tag:       "latest",
			},
			ClonerefsPath: "/clonerefs",
		}}
		buildSteps = append(buildSteps, step)
	}
	return buildSteps, nil
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

func buildRootImageStreamFromRepository(readFile readFile) (*api.ImageStreamTagReference, error) {
	data, err := readFile(api.CIOperatorInrepoConfigFileName)
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

func getClusterPoolPullSpec(ctx context.Context, claim *api.ClusterClaim, hiveClient ctrlruntimeclient.WithWatch) (string, error) {
	clusterPool, err := utils.ClusterPoolFromClaim(ctx, claim, hiveClient)
	if err != nil {
		return "", err
	}

	clusterImageSet := &hivev1.ClusterImageSet{}
	if err := hiveClient.Get(ctx, types.NamespacedName{Name: clusterPool.Spec.ImageSetRef.Name}, clusterImageSet); err != nil {
		return "", fmt.Errorf("failed to find cluster image set `%s` for cluster pool `%s`: %w", clusterPool.Spec.ImageSetRef.Name, clusterPool.Name, err)
	}

	return clusterImageSet.Spec.ReleaseImage, nil
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
	return &coreapi.ObjectReference{Kind: "ImageStreamTag", Namespace: "ocp", Name: majorMinor + ":cli"}, nil
}
