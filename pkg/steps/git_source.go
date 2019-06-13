package steps

import (
	"context"
	"fmt"
	"log"

	buildapi "github.com/openshift/api/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

type gitSourceStep struct {
	config      api.ProjectDirectoryImageBuildInputs
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	buildClient BuildClient
	artifactDir string
	jobSpec     *api.JobSpec
}

func (s *gitSourceStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (s *gitSourceStep) Run(ctx context.Context, dry bool) error {
	if s.jobSpec.Refs == nil {
		log.Printf("Nothing to build source image from, no refs")
		return nil
	}
	return handleBuild(s.buildClient, buildFromSource(s.jobSpec, "", api.PipelineImageStreamTagReferenceRoot, buildapi.BuildSource{
		Type:       buildapi.BuildSourceGit,
		ContextDir: s.config.ContextDir,
		Git: &buildapi.GitBuildSource{
			URI: fmt.Sprintf("https://github.com/%s/%s.git", s.jobSpec.Refs.Org, s.jobSpec.Refs.Repo),
			Ref: s.jobSpec.Refs.BaseRef,
		},
	}, s.config.DockerfilePath, s.resources), dry, s.artifactDir)
}

func (s *gitSourceStep) Done() (bool, error) {
	return imageStreamTagExists(api.PipelineImageStreamTagReferenceRoot, s.imageClient.ImageStreamTags(s.jobSpec.Namespace))
}

func (s *gitSourceStep) Name() string { return string(api.PipelineImageStreamTagReferenceRoot) }

func (s *gitSourceStep) Description() string {
	return fmt.Sprintf("Build git source code into an image and tag it as %s", api.PipelineImageStreamTagReferenceRoot)
}

func (s *gitSourceStep) Requires() []api.StepLink { return nil }

func (s *gitSourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)}
}

func (s *gitSourceStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

// GitSourceStep returns gitSourceStep that holds all the required information to create a build from a git source.
func GitSourceStep(config api.ProjectDirectoryImageBuildInputs, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageV1Interface, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return &gitSourceStep{
		config:      config,
		resources:   resources,
		buildClient: buildClient,
		imageClient: imageClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}
