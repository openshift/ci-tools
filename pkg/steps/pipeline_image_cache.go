package steps

import (
	"fmt"
	"strconv"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/ci-operator/pkg/api"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
)

func rawCommandDockerfile(from api.PipelineImageStreamTagReference, commands string) string {
	return fmt.Sprintf(`FROM %s:%s
RUN ["/bin/bash", "-c", "set -o errexit; umask 0002; %s"]`, PipelineImageStream, from, strconv.Quote(commands))
}

type pipelineImageCacheStep struct {
	config      api.PipelineImageCacheStepConfiguration
	buildClient buildclientset.BuildInterface
	istClient   imageclientset.ImageStreamTagInterface
	jobSpec     *JobSpec
}

func (s *pipelineImageCacheStep) Run(dry bool) error {
	dockerfile := rawCommandDockerfile(s.config.From, s.config.Commands)
	return handleBuild(s.buildClient, buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
		},
	), dry)
}

func (s *pipelineImageCacheStep) Done() (bool, error) {
	return imageStreamTagExists(s.config.To, s.istClient)
}

func (s *pipelineImageCacheStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *pipelineImageCacheStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func PipelineImageCacheStep(config api.PipelineImageCacheStepConfiguration, buildClient buildclientset.BuildInterface, istClient imageclientset.ImageStreamTagInterface, jobSpec *JobSpec) api.Step {
	return &pipelineImageCacheStep{
		config:      config,
		buildClient: buildClient,
		istClient:   istClient,
		jobSpec:     jobSpec,
	}
}
