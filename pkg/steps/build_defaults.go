package steps

import (
	"crypto/sha256"
	"fmt"
	"strings"

	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	templateapi "github.com/openshift/api/template/v1"
	appsclientset "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	templateclientset "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"

	"github.com/openshift/ci-operator/pkg/api"
)

const (
	// PipelineImageStream is the name of the
	// ImageStream used to hold images built
	// to cache build steps in the pipeline.
	PipelineImageStream = "pipeline"

	// DefaultRPMLocation is the default relative
	// directory for Origin-based projects to put
	// their built RPMs.
	DefaultRPMLocation = "_output/local/releases/rpms/"

	// RPMServeLocation is the location from which
	// we will serve RPMs after they are built.
	RPMServeLocation = "/srv/repo"

	StableImageStream = "stable"
)

// FromConfig interprets the human-friendly fields in
// the release build configuration and generates steps for
// them, returning the full set of steps requires for the
// build, including defaulted steps, generated steps and
// all raw steps that the user provided.
func FromConfig(config *api.ReleaseBuildConfiguration, jobSpec *JobSpec, templates []*templateapi.Template, paramFile string, clusterConfig *rest.Config) ([]api.Step, error) {
	var buildSteps []api.Step

	jobNamespace := jobSpec.Namespace()

	var buildClient BuildClient
	var buildRESTClient rest.Interface
	var imageStreamTagClient imageclientset.ImageStreamTagInterface
	var imageStreamGetter imageclientset.ImageStreamsGetter
	var imageStreamTagsGetter imageclientset.ImageStreamTagsGetter
	var imageStreamClient imageclientset.ImageStreamInterface
	var routeGetter routeclientset.RoutesGetter
	var routeClient routeclientset.RouteInterface
	var deploymentClient appsclientset.DeploymentConfigInterface
	var podClient coreclientset.PodInterface
	var serviceClient coreclientset.ServiceInterface
	var templateClient TemplateClient
	var configMapClient coreclientset.ConfigMapInterface

	if clusterConfig != nil {
		buildGetter, err := buildclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get build client for cluster config: %v", err)
		}
		buildRESTClient = buildGetter.RESTClient()
		buildClient = NewBuildClient(buildGetter.Builds(jobNamespace), buildRESTClient, jobNamespace)

		imageGetter, err := imageclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get image client for cluster config: %v", err)
		}
		imageStreamGetter = imageGetter
		imageStreamTagsGetter = imageGetter
		imageStreamTagClient = imageGetter.ImageStreamTags(jobNamespace)
		imageStreamClient = imageGetter.ImageStreams(jobNamespace)

		routeGetter, err = routeclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get route client for cluster config: %v", err)
		}
		routeClient = routeGetter.Routes(jobNamespace)

		templateGetter, err := templateclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get template client for cluster config: %v", err)
		}
		templateClient = NewTemplateClient(templateGetter, templateGetter.RESTClient(), jobNamespace)

		appsGetter, err := appsclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get apps client for cluster config: %v", err)
		}
		deploymentClient = appsGetter.DeploymentConfigs(jobNamespace)

		coreGetter, err := coreclientset.NewForConfig(clusterConfig)
		if err != nil {
			return buildSteps, fmt.Errorf("could not get core client for cluster config: %v", err)
		}
		serviceClient = coreGetter.Services(jobNamespace)
		configMapClient = coreGetter.ConfigMaps(jobNamespace)
		podClient = coreGetter.Pods(jobNamespace)
	}

	params := NewDeferredParameters()
	params.Add("JOB_NAME", nil, func() (string, error) { return jobSpec.Job, nil })
	params.Add("JOB_NAME_HASH", nil, func() (string, error) { return fmt.Sprintf("%x", sha256.Sum256([]byte(jobSpec.Job)))[:5], nil })
	params.Add("JOB_NAME_SAFE", nil, func() (string, error) { return strings.Replace(jobSpec.Job, "_", "-", -1), nil })
	params.Add("NAMESPACE", nil, func() (string, error) { return jobNamespace, nil })

	for _, rawStep := range stepConfigsForBuild(config, jobSpec) {
		var step api.Step
		if rawStep.InputImageTagStepConfiguration != nil {
			step = InputImageTagStep(*rawStep.InputImageTagStepConfiguration, imageStreamTagsGetter, jobSpec)
		} else if rawStep.PipelineImageCacheStepConfiguration != nil {
			step = PipelineImageCacheStep(*rawStep.PipelineImageCacheStepConfiguration, buildClient, imageStreamTagClient, jobSpec)
		} else if rawStep.SourceStepConfiguration != nil {
			step = SourceStep(*rawStep.SourceStepConfiguration, buildClient, imageStreamTagClient, jobSpec)
		} else if rawStep.ProjectDirectoryImageBuildStepConfiguration != nil {
			step = ProjectDirectoryImageBuildStep(*rawStep.ProjectDirectoryImageBuildStepConfiguration, buildClient, imageStreamTagClient, jobSpec)
		} else if rawStep.RPMImageInjectionStepConfiguration != nil {
			step = RPMImageInjectionStep(*rawStep.RPMImageInjectionStepConfiguration, buildClient, routeClient, imageStreamTagClient, jobSpec)
		} else if rawStep.RPMServeStepConfiguration != nil {
			step = RPMServerStep(*rawStep.RPMServeStepConfiguration, deploymentClient, routeClient, serviceClient, imageStreamTagClient, jobSpec)
		} else if rawStep.OutputImageTagStepConfiguration != nil {
			step = OutputImageTagStep(*rawStep.OutputImageTagStepConfiguration, imageStreamTagClient, imageStreamClient, jobSpec)
		} else if rawStep.ReleaseImagesTagStepConfiguration != nil {
			step = ReleaseImagesTagStep(*rawStep.ReleaseImagesTagStepConfiguration, imageStreamTagClient, imageStreamGetter, routeGetter, configMapClient, jobSpec)
		} else if rawStep.TestStepConfiguration != nil {
			step = TestStep(*rawStep.TestStepConfiguration, podClient, jobSpec)
		}
		provides, link := step.Provides()
		for name, fn := range provides {
			params.Add(name, link, fn)
		}
		buildSteps = append(buildSteps, step)
	}
	for _, template := range templates {
		step := TemplateExecutionStep(template, params, podClient, templateClient, jobSpec)
		buildSteps = append(buildSteps, step)
	}
	if len(paramFile) > 0 {
		buildSteps = append(buildSteps, WriteParametersStep(params, paramFile, jobSpec))
	}

	return buildSteps, nil
}

func stepConfigsForBuild(config *api.ReleaseBuildConfiguration, jobSpec *JobSpec) []api.StepConfiguration {
	var buildSteps []api.StepConfiguration

	if config.TestBaseImage != nil {
		if config.TestBaseImage.Namespace == "" {
			config.TestBaseImage.Namespace = jobSpec.baseNamespace
		}
		if config.TestBaseImage.Name == "" {
			config.TestBaseImage.Name = fmt.Sprintf("%s-test-base", jobSpec.Refs.Repo)
		}
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: *config.TestBaseImage,
			To:        api.PipelineImageStreamTagReferenceRoot,
		}})
	}

	buildSteps = append(buildSteps, api.StepConfiguration{SourceStepConfiguration: &api.SourceStepConfiguration{
		From: api.PipelineImageStreamTagReferenceRoot,
		To:   api.PipelineImageStreamTagReferenceSource,
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
			out = DefaultRPMLocation
		}

		buildSteps = append(buildSteps, api.StepConfiguration{PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     from,
			To:       api.PipelineImageStreamTagReferenceRPMs,
			Commands: fmt.Sprintf(`%s; ln -s $( pwd )/%s %s`, config.RpmBuildCommands, out, RPMServeLocation),
		}})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
			From: api.PipelineImageStreamTagReferenceRPMs,
		}})
	}

	for _, baseImage := range config.BaseImages {
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: baseImage,
			To:        api.PipelineImageStreamTagReference(baseImage.Name),
		}})
	}

	for _, baseRPMImage := range config.BaseRPMImages {
		as := baseRPMImage.As
		if len(as) == 0 {
			as = baseRPMImage.Name
		}
		intermediateTag := api.PipelineImageStreamTagReference(fmt.Sprintf("%s-without-rpms", as))
		buildSteps = append(buildSteps, api.StepConfiguration{InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
			BaseImage: baseRPMImage,
			To:        intermediateTag,
		}})

		buildSteps = append(buildSteps, api.StepConfiguration{RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
			From: intermediateTag,
			To:   api.PipelineImageStreamTagReference(as),
		}})
	}

	for i := range config.Images {
		image := &config.Images[i]
		buildSteps = append(buildSteps, api.StepConfiguration{ProjectDirectoryImageBuildStepConfiguration: image})
		if config.ReleaseTagConfiguration != nil && len(config.ReleaseTagConfiguration.Name) > 0 {
			buildSteps = append(buildSteps, api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
				From: image.To,
				To: api.ImageStreamTagReference{
					Name: fmt.Sprintf("%s%s", config.ReleaseTagConfiguration.NamePrefix, StableImageStream),
					Tag:  string(image.To),
				},
			}})
		} else {
			buildSteps = append(buildSteps, api.StepConfiguration{OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
				From: image.To,
				To: api.ImageStreamTagReference{
					Name: string(image.To),
					Tag:  "ci",
				},
			}})
		}
	}

	for i := range config.Tests {
		buildSteps = append(buildSteps, api.StepConfiguration{TestStepConfiguration: &config.Tests[i]})
	}

	if config.ReleaseTagConfiguration != nil {
		buildSteps = append(buildSteps, api.StepConfiguration{ReleaseImagesTagStepConfiguration: config.ReleaseTagConfiguration})
	}

	buildSteps = append(buildSteps, config.RawSteps...)

	return buildSteps
}
