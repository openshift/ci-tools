package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	templateapi "github.com/openshift/api/template/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
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
	"github.com/openshift/ci-tools/pkg/util/watchingclient"
)

type inputImageSet map[api.InputImageTagStepConfiguration]struct{}

// FromConfig interprets the human-friendly fields in
// the release build configuration and generates steps for
// them, returning the full set of steps requires for the
// build, including defaulted steps, generated steps and
// all raw steps that the user provided.
func FromConfig(
	ctx context.Context,
	config *api.ReleaseBuildConfiguration,
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
) ([]api.Step, []api.Step, error) {
	crclient, err := watchingclient.New(clusterConfig)
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

	var hiveClient ctrlruntimeclient.Client
	if hiveKubeconfig != nil {
		hiveClient, err = ctrlruntimeclient.New(hiveKubeconfig, ctrlruntimeclient.Options{})
		if err != nil {
			return nil, nil, fmt.Errorf("could not get Hive client for Hive kube config: %w", err)
		}
	}

	return fromConfig(ctx, config, jobSpec, templates, paramFile, promote, client, buildClient, templateClient, podClient, leaseClient, hiveClient, &http.Client{}, requiredTargets, cloneAuthConfig, pullSecret, pushSecret, api.NewDeferredParameters(nil))
}

func fromConfig(
	ctx context.Context,
	config *api.ReleaseBuildConfiguration,
	jobSpec *api.JobSpec,
	templates []*templateapi.Template,
	paramFile string,
	promote bool,
	client loggingclient.LoggingClient,
	buildClient steps.BuildClient,
	templateClient steps.TemplateClient,
	podClient steps.PodClient,
	leaseClient *lease.Client,
	hiveClient ctrlruntimeclient.Client,
	httpClient release.HTTPClient,
	requiredTargets []string,
	cloneAuthConfig *steps.CloneAuthConfig,
	pullSecret, pushSecret *coreapi.Secret,
	params *api.DeferredParameters,
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
	resolver := rootImageResolver(client, ctx)
	rawSteps, err := stepConfigsForBuild(config, jobSpec, ioutil.ReadFile, resolver)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stepConfigsForBuild: %w", err)
	}
	for _, rawStep := range rawSteps {
		if testStep := rawStep.TestStepConfiguration; testStep != nil {
			steps, err := stepForTest(config, params, podClient, leaseClient, templateClient, client, hiveClient, jobSpec, inputImages, testStep)
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
			if env := utils.ReleaseImageEnv(resolveConfig.Name); params.HasInput(env) {
				value, err = params.Get(env)
				if err != nil {
					return nil, nil, results.ForReason("resolving_release").ForError(fmt.Errorf("failed to get %q parameter: %w", env, err))
				}
				logrus.Infof("Using explicitly provided pull-spec for release %s (%s)", resolveConfig.Name, value)
			} else {
				switch {
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
				logrus.Infof("Resolved release %s to %s", resolveConfig.Name, value)
			}
			step := releasesteps.ImportReleaseStep(resolveConfig.Name, value, false, config.Resources, podClient, jobSpec, pullSecret)
			buildSteps = append(buildSteps, step)
			addProvidesForStep(step, params)
			continue
		}
		var step api.Step
		var stepLinks []api.StepLink
		if rawStep.InputImageTagStepConfiguration != nil {
			conf := *rawStep.InputImageTagStepConfiguration
			if _, ok := inputImages[conf]; ok {
				continue
			}
			step = steps.InputImageTagStep(conf, client, jobSpec)
			inputImages[conf] = struct{}{}
		} else if rawStep.PipelineImageCacheStepConfiguration != nil {
			step = steps.PipelineImageCacheStep(*rawStep.PipelineImageCacheStepConfiguration, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.SourceStepConfiguration != nil {
			step = steps.SourceStep(*rawStep.SourceStepConfiguration, config.Resources, buildClient, jobSpec, cloneAuthConfig, pullSecret)
		} else if rawStep.BundleSourceStepConfiguration != nil {
			step = steps.BundleSourceStep(*rawStep.BundleSourceStepConfiguration, config, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.IndexGeneratorStepConfiguration != nil {
			step = steps.IndexGeneratorStep(*rawStep.IndexGeneratorStepConfiguration, config, config.Resources, buildClient, jobSpec, pullSecret)
		} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
			step = steps.ProjectDirectoryImageBuildStep(*rawStep.ProjectDirectoryImageBuildStepConfiguration, config, config.Resources, podClient, buildClient, jobSpec, pullSecret)
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
					releaseStep = releasesteps.ImportReleaseStep(name, pullSpec, true, config.Resources, podClient, jobSpec, pullSecret)
				} else {
					releaseStep = releasesteps.AssembleReleaseStep(name, rawStep.ReleaseImagesTagStepConfiguration, config.Resources, podClient, jobSpec)
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
					Env:          steps.DefaultLeaseEnv,
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
	config *api.ReleaseBuildConfiguration,
	params *api.DeferredParameters,
	podClient steps.PodClient,
	leaseClient *lease.Client,
	templateClient steps.TemplateClient,
	client loggingclient.LoggingClient,
	hiveClient ctrlruntimeclient.Client,
	jobSpec *api.JobSpec,
	inputImages inputImageSet,
	c *api.TestStepConfiguration,
) ([]api.Step, error) {
	if test := c.MultiStageTestConfigurationLiteral; test != nil {
		leases := leasesForTest(test)
		if len(leases) != 0 {
			params = api.NewDeferredParameters(params)
		}
		step := steps.MultiStageTestStep(*c, config, params, podClient, jobSpec, leases)
		if len(leases) != 0 {
			step = steps.LeaseStep(leaseClient, leases, step, jobSpec.Namespace)
			addProvidesForStep(step, params)
		}
		if c.ClusterClaim != nil {
			step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step)
		}
		return append([]api.Step{step}, stepsForStepImages(client, jobSpec, inputImages, test)...), nil
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
			Env:          steps.DefaultLeaseEnv,
			Count:        1,
		}}, step, jobSpec.Namespace)
		addProvidesForStep(step, params)
		return []api.Step{step}, nil
	}
	step := steps.TestStep(*c, config.Resources, podClient, jobSpec)
	if c.ClusterClaim != nil {
		step = steps.ClusterClaimStep(c.As, c.ClusterClaim, hiveClient, client, jobSpec, step)
	}
	return []api.Step{step}, nil
}

// stepsForStepImages creates steps that import images referenced in test steps.
func stepsForStepImages(
	client loggingclient.LoggingClient,
	jobSpec *api.JobSpec,
	inputImages inputImageSet,
	test *api.MultiStageTestConfigurationLiteral,
) (ret []api.Step) {
	for _, subStep := range append(append(test.Pre, test.Test...), test.Post...) {
		if link, ok := subStep.FromImageTag(); ok {
			config := api.InputImageTagStepConfiguration{
				BaseImage: *subStep.FromImage,
				To:        link,
			}
			if _, ok := inputImages[config]; ok {
				continue
			}
			inputImages[config] = struct{}{}
			ret = append(ret, steps.InputImageTagStep(config, client, jobSpec))
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
		step = steps.NewInputEnvironmentStep(step.Name(), values, step.Creates())
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

// leasesForTest aggregates all the lease configurations in a test.
// It is assumed that they have been validated and contain only valid and
// unique values.
func leasesForTest(s *api.MultiStageTestConfigurationLiteral) (ret []api.StepLease) {
	if p := s.ClusterProfile; p != "" {
		ret = append(ret, api.StepLease{
			ResourceType: p.LeaseType(),
			Env:          steps.DefaultLeaseEnv,
			Count:        1,
		})
	}
	for _, step := range append(s.Pre, append(s.Test, s.Post...)...) {
		ret = append(ret, step.Leases...)
	}
	ret = append(ret, s.Leases...)
	return
}

// rootImageResolver creates a resolver for the root image import step. We attempt to resolve the root image and
// the build cache. If we are able to successfully determine that the build cache is up-to-date, we import it as
// the root image.
func rootImageResolver(client loggingclient.LoggingClient, ctx context.Context) func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
	return func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error) {
		logrus.Debugf("Determining if build cache %s can be used in place of root %s\n", cache.ISTagName(), root.ISTagName())
		cacheTag := &imagev1.ImageStreamTag{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: cache.Namespace, Name: fmt.Sprintf("%s:%s", cache.Name, cache.Tag)}, cacheTag); err != nil {
			if kapierrors.IsNotFound(err) {
				logrus.Debugf("Build cache %s not found, falling back to %s\n", cache.ISTagName(), root.ISTagName())
				// no build cache, use the normal root
				return root, nil
			}
			return nil, fmt.Errorf("could not resolve build cache image stream tag %s: %w", cache.ISTagName(), err)
		}
		logrus.Debugf("Resolved build cache %s to %s\n", cache.ISTagName(), cacheTag.Image.Name)
		metadata := &docker10.DockerImage{}
		if len(cacheTag.Image.DockerImageMetadata.Raw) == 0 {
			return nil, fmt.Errorf("could not fetch Docker image metadata build cache %s", cache.ISTagName())
		}
		if err := json.Unmarshal(cacheTag.Image.DockerImageMetadata.Raw, metadata); err != nil {
			return nil, fmt.Errorf("malformed Docker image metadata on build cache %s: %w", cache.ISTagName(), err)
		}
		prior := metadata.Config.Labels[api.ImageVersionLabel(api.PipelineImageStreamTagReferenceRoot)]
		logrus.Debugf("Build cache %s is based on root image at %s\n", cache.ISTagName(), prior)

		rootTag := &imagev1.ImageStreamTag{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: root.Namespace, Name: fmt.Sprintf("%s:%s", root.Name, root.Tag)}, rootTag); err != nil {
			return nil, fmt.Errorf("could not resolve build root image stream tag %s: %w", root.ISTagName(), err)
		}
		logrus.Debugf("Resolved root image %s to %s\n", root.ISTagName(), rootTag.Image.Name)
		current := rootTag.Image.Name
		if prior == current {
			logrus.Debugf("Using build cache %s as root image.\n", cache.ISTagName())
			return cache, nil
		}
		logrus.Debugf("Using default image %s as root image.\n", root.ISTagName())
		return root, nil
	}
}

type readFile func(string) ([]byte, error)
type resolveRoot func(root, cache *api.ImageStreamTagReference) (*api.ImageStreamTagReference, error)

func stepConfigsForBuild(config *api.ReleaseBuildConfiguration, jobSpec *api.JobSpec, readFile readFile, resolveRoot resolveRoot) ([]api.StepConfiguration, error) {
	var buildSteps []api.StepConfiguration

	if config.InputConfiguration.BaseImages == nil {
		config.InputConfiguration.BaseImages = make(map[string]api.ImageStreamTagReference)
	}
	if config.InputConfiguration.BaseRPMImages == nil {
		config.InputConfiguration.BaseRPMImages = make(map[string]api.ImageStreamTagReference)
	}

	// ensure the "As" field is set to the provided alias.
	for alias, target := range config.InputConfiguration.BaseImages {
		target.As = alias
		config.InputConfiguration.BaseImages[alias] = target
	}
	for alias, target := range config.InputConfiguration.BaseRPMImages {
		target.As = alias
		config.InputConfiguration.BaseRPMImages[alias] = target
	}

	if target := config.InputConfiguration.BuildRootImage; target != nil {
		if target.FromRepository {
			istTagRef, err := buildRootImageStreamFromRepository(readFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read buildRootImageStream from repository: %w", err)
			}
			target.ImageStreamTagReference = istTagRef
		}
		if isTagRef := target.ImageStreamTagReference; isTagRef != nil {
			if config.InputConfiguration.BuildRootImage.UseBuildCache {
				cache := api.BuildCacheFor(config.Metadata)
				root, err := resolveRoot(isTagRef, &cache)
				if err != nil {
					return nil, fmt.Errorf("could not resolve build root: %w", err)
				}
				isTagRef = root
			}
			buildSteps = append(buildSteps, api.StepConfiguration{
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: *isTagRef,
					To:        api.PipelineImageStreamTagReferenceRoot,
				}})
		} else if gitSourceRef := target.ProjectImageBuild; gitSourceRef != nil {
			buildSteps = append(buildSteps, api.StepConfiguration{
				ProjectDirectoryImageBuildInputs: gitSourceRef,
			})
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
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: defaultImageFromReleaseTag(baseImage, config.ReleaseTagConfiguration),
			To:        api.PipelineImageStreamTagReference(alias),
		}})
	}

	for alias, target := range config.InputConfiguration.BaseRPMImages {
		intermediateTag := api.PipelineImageStreamTagReference(fmt.Sprintf("%s-without-rpms", alias))
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: defaultImageFromReleaseTag(target, config.ReleaseTagConfiguration),
			To:        intermediateTag,
		}})

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

func defaultImageFromReleaseTag(base api.ImageStreamTagReference, release *api.ReleaseTagConfiguration) api.ImageStreamTagReference {
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

func buildRootImageStreamFromRepository(readFile readFile) (*api.ImageStreamTagReference, error) {
	data, err := readFile(api.CIOperatorInrepoConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s file: %w", api.CIOperatorInrepoConfigFileName, err)
	}
	config := api.CIOperatorInrepoConfig{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", api.CIOperatorInrepoConfigFileName, err)
	}
	return &config.BuildRootImage, nil
}
