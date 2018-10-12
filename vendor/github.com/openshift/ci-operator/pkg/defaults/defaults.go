package defaults

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	appsclientset "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	templateapi "github.com/openshift/api/template/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	"github.com/openshift/ci-operator/pkg/api"
	"github.com/openshift/ci-operator/pkg/steps"
	"github.com/openshift/ci-operator/pkg/steps/release"
)

// FromConfig interprets the human-friendly fields in
// the release build configuration and generates steps for
// them, returning the full set of steps requires for the
// build, including defaulted steps, generated steps and
// all raw steps that the user provided.
func FromConfig(
	config *api.ReleaseBuildConfiguration,
	jobSpec *api.JobSpec,
	templates []*templateapi.Template,
	paramFile, artifactDir string,
	promote bool,
	clusterConfig *rest.Config,
	requiredTargets []string,
) ([]api.Step, []api.Step, error) {
	var buildSteps []api.Step
	var postSteps []api.Step

	requiredNames := make(map[string]struct{})
	for _, target := range requiredTargets {
		requiredNames[target] = struct{}{}
	}

	var buildClient steps.BuildClient
	var imageClient imageclientset.ImageV1Interface
	var routeGetter routeclientset.RoutesGetter
	var deploymentGetter appsclientset.DeploymentsGetter
	var templateClient steps.TemplateClient
	var configMapGetter coreclientset.ConfigMapsGetter
	var serviceGetter coreclientset.ServicesGetter
	var podClient steps.PodClient

	if clusterConfig != nil {
		buildGetter, err := buildclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get build client for cluster config: %v", err)
		}
		buildClient = steps.NewBuildClient(buildGetter, buildGetter.RESTClient())

		imageGetter, err := imageclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get image client for cluster config: %v", err)
		}
		imageClient = imageGetter

		routeGetter, err = routeclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get route client for cluster config: %v", err)
		}

		templateGetter, err := templateclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get template client for cluster config: %v", err)
		}
		templateClient = steps.NewTemplateClient(templateGetter, templateGetter.RESTClient())

		appsGetter, err := appsclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get apps client for cluster config: %v", err)
		}
		deploymentGetter = appsGetter

		coreGetter, err := coreclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get core client for cluster config: %v", err)
		}
		serviceGetter = coreGetter
		configMapGetter = coreGetter

		podClient = steps.NewPodClient(coreGetter, clusterConfig, coreGetter.RESTClient())
	}

	params := steps.NewDeferredParameters()
	params.Add("JOB_NAME", nil, func() (string, error) { return jobSpec.Job, nil })
	params.Add("JOB_NAME_HASH", nil, func() (string, error) { return fmt.Sprintf("%x", sha256.Sum256([]byte(jobSpec.Job)))[:5], nil })
	params.Add("JOB_NAME_SAFE", nil, func() (string, error) { return strings.Replace(jobSpec.Job, "_", "-", -1), nil })
	params.Add("NAMESPACE", nil, func() (string, error) { return jobSpec.Namespace, nil })

	var imageStepLinks []api.StepLink
	var releaseStep api.Step
	for _, rawStep := range stepConfigsForBuild(config, jobSpec) {
		var step api.Step
		var stepLinks []api.StepLink
		if rawStep.InputImageTagStepConfiguration != nil {
			srcClient, err := anonymousClusterImageStreamClient(imageClient, clusterConfig, rawStep.InputImageTagStepConfiguration.BaseImage.Cluster)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to access image stream tag on remote cluster: %v", err)
			}
			step = steps.InputImageTagStep(*rawStep.InputImageTagStepConfiguration, srcClient, imageClient, jobSpec)
		} else if rawStep.PipelineImageCacheStepConfiguration != nil {
			step = steps.PipelineImageCacheStep(*rawStep.PipelineImageCacheStepConfiguration, config.Resources, buildClient, imageClient, jobSpec)
		} else if rawStep.SourceStepConfiguration != nil {
			srcClient, err := anonymousClusterImageStreamClient(imageClient, clusterConfig, rawStep.SourceStepConfiguration.ClonerefsImage.Cluster)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to access image stream tag on remote cluster: %v", err)
			}
			step = steps.SourceStep(*rawStep.SourceStepConfiguration, config.Resources, buildClient, srcClient, imageClient, jobSpec)
		} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
			step = steps.ProjectDirectoryImageBuildStep(*rawStep.ProjectDirectoryImageBuildStepConfiguration, config.Resources, buildClient, imageClient, imageClient, jobSpec)
		} else if rawStep.ProjectDirectoryImageBuildInputs != nil {
			step = steps.GitSourceStep(*rawStep.ProjectDirectoryImageBuildInputs, config.Resources, buildClient, imageClient, jobSpec)
		} else if rawStep.RPMImageInjectionStepConfiguration != nil {
			step = steps.RPMImageInjectionStep(*rawStep.RPMImageInjectionStepConfiguration, config.Resources, buildClient, routeGetter, imageClient, jobSpec)
		} else if rawStep.RPMServeStepConfiguration != nil {
			step = steps.RPMServerStep(*rawStep.RPMServeStepConfiguration, deploymentGetter, routeGetter, serviceGetter, imageClient, jobSpec)
		} else if rawStep.OutputImageTagStepConfiguration != nil {
			step = steps.OutputImageTagStep(*rawStep.OutputImageTagStepConfiguration, imageClient, imageClient, jobSpec)
			// all required or non-optional output images are considered part of [images]
			if _, ok := requiredNames[string(rawStep.OutputImageTagStepConfiguration.From)]; ok || !rawStep.OutputImageTagStepConfiguration.Optional {
				stepLinks = append(stepLinks, step.Creates()...)
			}
		} else if rawStep.ReleaseImagesTagStepConfiguration != nil {
			srcClient, err := anonymousClusterImageStreamClient(imageClient, clusterConfig, rawStep.ReleaseImagesTagStepConfiguration.Cluster)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to access release images on remote cluster: %v", err)
			}
			step = steps.ReleaseImagesTagStep(*rawStep.ReleaseImagesTagStepConfiguration, srcClient, imageClient, routeGetter, configMapGetter, params, jobSpec)
			stepLinks = append(stepLinks, step.Creates()...)

			releaseStep = release.AssembleReleaseStep(*rawStep.ReleaseImagesTagStepConfiguration, podClient, imageClient, artifactDir, jobSpec)

		} else if rawStep.TestStepConfiguration != nil {
			step = steps.TestStep(*rawStep.TestStepConfiguration, config.Resources, podClient, artifactDir, jobSpec)
		}

		step, ok := checkForFullyQualifiedStep(step, params)
		if !ok {
			imageStepLinks = append(imageStepLinks, stepLinks...)
		}
		buildSteps = append(buildSteps, step)
	}

	for _, template := range templates {
		step := steps.TemplateExecutionStep(template, params, podClient, templateClient, artifactDir, jobSpec)
		buildSteps = append(buildSteps, step)
	}

	if len(paramFile) > 0 {
		buildSteps = append(buildSteps, steps.WriteParametersStep(params, paramFile, jobSpec))
	}

	if releaseStep != nil {
		releaseStep, _ = checkForFullyQualifiedStep(releaseStep, params)
		buildSteps = append(buildSteps, releaseStep)
	} else {
		buildSteps = append(buildSteps, steps.StableImagesTagStep(imageClient, jobSpec))
	}

	buildSteps = append(buildSteps, steps.ImagesReadyStep(imageStepLinks, jobSpec))

	if promote {
		cfg, err := promotionDefaults(config)
		if err != nil {
			return nil, nil, fmt.Errorf("could not determine promotion defaults: %v", err)
		}
		var tags []string
		for _, image := range config.Images {
			// if the image is required or non-optional, include it in promotion
			if _, ok := requiredNames[string(image.To)]; ok || !image.Optional {
				tags = append(tags, string(image.To))
			}
		}
		postSteps = append(postSteps, steps.PromotionStep(*cfg, tags, imageClient, imageClient, jobSpec))
	}

	return buildSteps, postSteps, nil
}

// checkForFullyQualifiedStep if all output parameters of this step are part of the
// environment, replace the step with a shim that automatically provides those variables.
// Returns true if the step was replaced.
func checkForFullyQualifiedStep(step api.Step, params *steps.DeferredParameters) (api.Step, bool) {
	provides, link := step.Provides()

	if values, ok := envHasAllParameters(provides); ok {
		log.Printf("Task %s is satisfied by environment variables and will be skipped", step.Name())
		step = steps.NewInputEnvironmentStep(step.Name(), values, step.Creates())
		for k, v := range values {
			params.Set(k, v)
		}
		return step, true
	}
	for name, fn := range provides {
		params.Add(name, link, fn)
	}
	return step, false
}

func promotionDefaults(configSpec *api.ReleaseBuildConfiguration) (*api.PromotionConfiguration, error) {
	config := configSpec.PromotionConfiguration
	if config == nil {
		config = &api.PromotionConfiguration{}
	}
	if len(config.Tag) == 0 && len(config.Name) == 0 {
		if input := configSpec.ReleaseTagConfiguration; input != nil {
			config.Namespace = input.Namespace
			config.Name = input.Name
			config.NamePrefix = input.NamePrefix
			config.Tag = input.Tag
		}
	}
	if config == nil {
		return nil, fmt.Errorf("cannot promote images, no promotion or release tag configuration defined")
	}
	return config, nil
}

func anonymousClusterImageStreamClient(client imageclientset.ImageV1Interface, config *rest.Config, overrideClusterHost string) (imageclientset.ImageV1Interface, error) {
	if config == nil || len(overrideClusterHost) == 0 {
		return client, nil
	}
	if equivalentHosts(config.Host, overrideClusterHost) {
		return client, nil
	}
	newConfig := rest.AnonymousClientConfig(config)
	newConfig.TLSClientConfig = rest.TLSClientConfig{}
	newConfig.Host = overrideClusterHost
	return imageclientset.NewForConfig(newConfig)
}

func equivalentHosts(a, b string) bool {
	a = normalizeURL(a)
	b = normalizeURL(b)
	return a == b
}

func normalizeURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	if u.Scheme == "https" {
		u.Scheme = ""
	}
	if strings.HasSuffix(u.Host, ":443") {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	if u.Scheme == "" && u.Path == "" && u.Fragment == "" && u.RawQuery == "" {
		return u.Host
	}
	return s
}

func stepConfigsForBuild(config *api.ReleaseBuildConfiguration, jobSpec *api.JobSpec) []api.StepConfiguration {
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
		if isTagRef := target.ImageStreamTagReference; isTagRef != nil {
			buildSteps = append(buildSteps, createStepConfigForTagRefImage(*isTagRef, jobSpec))
		} else if gitSourceRef := target.ProjectImageBuild; gitSourceRef != nil {
			buildSteps = append(buildSteps, createStepConfigForGitSource(*gitSourceRef, jobSpec))
		}
	}

	buildSteps = append(buildSteps, api.StepConfiguration{SourceStepConfiguration: &api.SourceStepConfiguration{
		From:      api.PipelineImageStreamTagReferenceRoot,
		To:        api.PipelineImageStreamTagReferenceSource,
		PathAlias: config.CanonicalGoRepository,
		ClonerefsImage: api.ImageStreamTagReference{
			Cluster:   "https://api.ci.openshift.org",
			Namespace: "ci",
			Name:      "clonerefs",
			Tag:       "latest",
		},
		ClonerefsPath: "/clonerefs",
	}})

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
			BaseImage: baseImage,
			To:        api.PipelineImageStreamTagReference(alias),
		}})
	}

	for alias, target := range config.InputConfiguration.BaseRPMImages {
		intermediateTag := api.PipelineImageStreamTagReference(fmt.Sprintf("%s-without-rpms", alias))
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: target,
			To:        intermediateTag,
		}})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
			From: intermediateTag,
			To:   api.PipelineImageStreamTagReference(alias),
		}})
	}

	for i := range config.Images {
		image := &config.Images[i]
		buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image})
		if config.ReleaseTagConfiguration != nil {
			if len(config.ReleaseTagConfiguration.Name) > 0 {
				buildSteps = append(buildSteps, api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
					From: image.To,
					To: api.ImageStreamTagReference{
						Name: fmt.Sprintf("%s%s", config.ReleaseTagConfiguration.NamePrefix, api.StableImageStream),
						Tag:  string(image.To),
					},
					Optional: image.Optional,
				}})
			} else {
				buildSteps = append(buildSteps, api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
					From: image.To,
					To: api.ImageStreamTagReference{
						Name: string(image.To),
						Tag:  "ci",
					},
					Optional: image.Optional,
				}})
			}
		} else {
			buildSteps = append(buildSteps, api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
				From: image.To,
				To: api.ImageStreamTagReference{
					Name: api.StableImageStream,
					Tag:  string(image.To),
				},
				Optional: image.Optional,
			}})
		}
	}

	for i := range config.Tests {
		test := &config.Tests[i]
		if test.ContainerTestConfiguration != nil {
			buildSteps = append(buildSteps, api.StepConfiguration{TestStepConfiguration: test})
		}
	}

	if config.ReleaseTagConfiguration != nil {
		buildSteps = append(buildSteps, api.StepConfiguration{ReleaseImagesTagStepConfiguration: config.ReleaseTagConfiguration})
	}

	buildSteps = append(buildSteps, config.RawSteps...)

	return buildSteps
}

func createStepConfigForTagRefImage(target api.ImageStreamTagReference, jobSpec *api.JobSpec) api.StepConfiguration {
	if target.Namespace == "" {
		target.Namespace = jobSpec.BaseNamespace
	}
	if target.Name == "" {
		target.Name = fmt.Sprintf("%s-test-base", jobSpec.Refs.Repo)
	}

	return api.StepConfiguration{
		InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: target,
			To:        api.PipelineImageStreamTagReferenceRoot,
		}}
}

func createStepConfigForGitSource(target api.ProjectDirectoryImageBuildInputs, jobSpec *api.JobSpec) api.StepConfiguration {
	return api.StepConfiguration{
		ProjectDirectoryImageBuildInputs: &api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: target.DockerfilePath,
			ContextDir:     target.ContextDir,
		},
	}
}

func envHasAllParameters(params map[string]func() (string, error)) (map[string]string, bool) {
	if len(params) == 0 {
		return nil, false
	}
	var values map[string]string
	for k := range params {
		v, ok := os.LookupEnv(k)
		if !ok {
			return nil, false
		}
		if values == nil {
			values = make(map[string]string)
		}
		values[k] = v
	}
	return values, true
}
