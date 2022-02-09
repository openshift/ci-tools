package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	coreapi "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

type projectDirectoryImageBuildStep struct {
	config             api.ProjectDirectoryImageBuildStepConfiguration
	releaseBuildConfig *api.ReleaseBuildConfiguration
	resources          api.ResourceConfiguration
	client             BuildClient
	jobSpec            *api.JobSpec
	pullSecret         *coreapi.Secret
}

func (s *projectDirectoryImageBuildStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *projectDirectoryImageBuildStep) Validate() error { return nil }

func (s *projectDirectoryImageBuildStep) Run(ctx context.Context) error {
	return results.ForReason("building_project_image").ForError(s.run(ctx))
}

func (s *projectDirectoryImageBuildStep) run(ctx context.Context) error {
	sourceTag, images, err := imagesFor(s.config, func(tag string) (string, error) {
		return getWorkingDir(s.client, tag, s.jobSpec.Namespace())
	}, s.releaseBuildConfig.IsBundleImage)
	if err != nil {
		return err
	}
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, sourceTag, s.jobSpec)
	if err != nil {
		return err
	}
	build := buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceImage,
			Dockerfile: s.config.DockerfileLiteral,
			Images:     images,
		},
		fromDigest,
		s.config.DockerfilePath,
		s.resources,
		s.pullSecret,
		s.config.BuildArgs,
	)
	return handleBuild(ctx, s.client, *build)
}

type workingDir func(tag string) (string, error)
type isBundleImage func(tag string) bool

func imagesFor(config api.ProjectDirectoryImageBuildStepConfiguration, workingDir workingDir, isBundleImage isBundleImage) (api.PipelineImageStreamTagReference, []buildapi.ImageSource, error) {
	images := buildInputsFromStep(config.Inputs)
	var sourceTag api.PipelineImageStreamTagReference
	var contextDir string
	if isBundleImage(string(config.To)) {
		// use the operator bundle source for bundle images
		sourceTag = api.PipelineImageStreamTagReferenceBundleSource
		contextDir = config.ContextDir
	} else if api.IsIndexImage(string(config.To)) {
		// use the index source for index images
		sourceTag = api.IndexGeneratorName(config.To)
	} else {
		// default to using the normal pipeline source image
		sourceTag = api.PipelineImageStreamTagReferenceSource
		contextDir = config.ContextDir
	}
	if _, overwritten := config.Inputs[string(sourceTag)]; !overwritten {
		// if the user has not overwritten the source, we need to make sure it's mounted in
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, sourceTag)
		baseDir, err := workingDir(source)
		if err != nil {
			return "", nil, fmt.Errorf("failed to get workingDir: %w", err)
		}
		images = append(images, buildapi.ImageSource{
			From: coreapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: source,
			},
			Paths: []buildapi.ImageSourcePath{{
				SourcePath:     fmt.Sprintf("%s/.", path.Join(baseDir, contextDir)),
				DestinationDir: ".",
			}},
		})
	}
	return sourceTag, images, nil
}

func getWorkingDir(client ctrlruntimeclient.Client, source, namespace string) (string, error) {
	ist := &imagev1.ImageStreamTag{}
	if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: source}, ist); err != nil {
		return "", fmt.Errorf("could not fetch source ImageStreamTag: %w", err)
	}
	metadata := &docker10.DockerImage{}
	if len(ist.Image.DockerImageMetadata.Raw) == 0 {
		return "", fmt.Errorf("could not fetch Docker image metadata for ImageStreamTag %s", source)
	}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, metadata); err != nil {
		return "", fmt.Errorf("malformed Docker image metadata on ImageStreamTag: %w", err)
	}
	return metadata.Config.WorkingDir, nil
}

func (s *projectDirectoryImageBuildStep) Requires() []api.StepLink {
	links := []api.StepLink{
		api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
	}
	if len(s.config.From) > 0 {
		links = append(links, api.InternalImageLink(s.config.From))
	}
	if s.releaseBuildConfig.IsBundleImage(string(s.config.To)) {
		links = append(links, api.InternalImageLink(api.PipelineImageStreamTagReferenceBundleSource))
	}
	if api.IsIndexImage(string(s.config.To)) {
		links = append(links, api.InternalImageLink(api.IndexGeneratorName(s.config.To)))
	}
	for name := range s.config.Inputs {
		links = append(links, api.InternalImageLink(api.PipelineImageStreamTagReference(name), api.StepLinkWithUnsatisfiableErrorMessage(fmt.Sprintf("%q is neither an imported nor a built image", name))))
	}
	return links
}

func (s *projectDirectoryImageBuildStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *projectDirectoryImageBuildStep) Provides() api.ParameterMap {
	if len(s.config.To) == 0 {
		return nil
	}
	return api.ParameterMap{
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *projectDirectoryImageBuildStep) Name() string { return s.config.TargetName() }

func (s *projectDirectoryImageBuildStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", s.config.To)
}

func (s *projectDirectoryImageBuildStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func ProjectDirectoryImageBuildStep(config api.ProjectDirectoryImageBuildStepConfiguration, releaseBuildConfig *api.ReleaseBuildConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &projectDirectoryImageBuildStep{
		config:             config,
		releaseBuildConfig: releaseBuildConfig,
		resources:          resources,
		client:             buildClient,
		jobSpec:            jobSpec,
		pullSecret:         pullSecret,
	}
}
