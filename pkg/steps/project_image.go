package steps

import (
	"encoding/json"
	"fmt"
	"log"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/api/image/docker10"
	"github.com/openshift/ci-operator/pkg/api"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type projectDirectoryImageBuildStep struct {
	config      api.ProjectDirectoryImageBuildStepConfiguration
	buildClient buildclientset.BuildInterface
	istClient   imageclientset.ImageStreamTagInterface
	jobSpec     *JobSpec
}

func (s *projectDirectoryImageBuildStep) Run() error {
	log.Printf("Creating build for %s/%s:%s", s.jobSpec.Identifier(), PipelineImageStream, s.config.To)
	source := fmt.Sprintf("%s:%s", PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
	ist, err := s.istClient.Get(source, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not fetch source ImageStreamTag: %v", err)
	}
	metadata := &docker10.DockerImage{}
	if len(ist.Image.DockerImageMetadata.Raw) == 0 {
		return fmt.Errorf("could not fetch Docker image metadata for ImageStreamTag %s", source)
	}
	if err := json.Unmarshal(ist.Image.DockerImageMetadata.Raw, metadata); err != nil {
		return fmt.Errorf("malformed Docker image metadata on ImageStreamTag: %v", err)
	}
	build, err := s.buildClient.Create(buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type: buildapi.BuildSourceImage,
			Images: []buildapi.ImageSource{{
				From: coreapi.ObjectReference{
					Kind: "ImageStreamTag",
					Name: source,
				},
				Paths: []buildapi.ImageSourcePath{{
					SourcePath:     fmt.Sprintf("%s/%s", metadata.Config.WorkingDir, s.config.ContextDir),
					DestinationDir: ".",
				}},
			}},
		},
	))
	if ! errors.IsAlreadyExists(err) {
		return err
	}
	return waitForBuild(s.buildClient, build.Name)
}

func (s *projectDirectoryImageBuildStep) Done() (bool, error) {
	return imageStreamTagExists(s.config.To, s.istClient)
}

func (s *projectDirectoryImageBuildStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *projectDirectoryImageBuildStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func ProjectDirectoryImageBuildStep(config api.ProjectDirectoryImageBuildStepConfiguration, buildClient buildclientset.BuildInterface, istClient imageclientset.ImageStreamTagInterface, jobSpec *JobSpec) api.Step {
	return &projectDirectoryImageBuildStep{
		config:      config,
		buildClient: buildClient,
		istClient:   istClient,
		jobSpec:     jobSpec,
	}
}
