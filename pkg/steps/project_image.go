package steps

import (
	"context"
	"encoding/json"
	"fmt"

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

func (*projectDirectoryImageBuildStep) Validate() error { return nil }

func (s *projectDirectoryImageBuildStep) Run(ctx context.Context) error {
	return results.ForReason("building_project_image").ForError(s.run(ctx))
}

func (s *projectDirectoryImageBuildStep) run(ctx context.Context) error {
	images := buildInputsFromStep(s.config.Inputs)
	// If image being built is an operator bundle, use the bundle source instead of original source
	if s.releaseBuildConfig.IsBundleImage(string(s.config.To)) {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceBundleSource)
		workingDir, err := getWorkingDir(s.client, source, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to get workingDir: %w", err)
		}
		images = append(images, buildapi.ImageSource{
			From: coreapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: source,
			},
			Paths: []buildapi.ImageSourcePath{{
				SourcePath:     fmt.Sprintf("%s/%s/.", workingDir, s.config.ContextDir),
				DestinationDir: ".",
			}},
		})
	} else if api.IsIndexImage(string(s.config.To)) {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.IndexGeneratorName(s.config.To))
		workingDir, err := getWorkingDir(s.client, source, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to get workingDir: %w", err)
		}
		images = append(images, buildapi.ImageSource{
			From: coreapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: source,
			},
			Paths: []buildapi.ImageSourcePath{{
				SourcePath:     fmt.Sprintf("%s/.", workingDir),
				DestinationDir: ".",
			}},
		})
	} else if _, ok := s.config.Inputs["src"]; !ok {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
		workingDir, err := getWorkingDir(s.client, source, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to get workingDir: %w", err)
		}
		images = append(images, buildapi.ImageSource{
			From: coreapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: source,
			},
			Paths: []buildapi.ImageSourcePath{{
				SourcePath:     fmt.Sprintf("%s/%s/.", workingDir, s.config.ContextDir),
				DestinationDir: ".",
			}},
		})
	}
	build := buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceImage,
			Dockerfile: s.config.DockerfileLiteral,
			Images:     images,
		},
		s.config.DockerfilePath,
		s.resources,
		s.pullSecret,
	)
	return handleBuild(ctx, s.client, build)
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

func (s *projectDirectoryImageBuildStep) Name() string { return string(s.config.To) }

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
