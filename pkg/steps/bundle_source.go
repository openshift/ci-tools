package steps

import (
	"context"
	"fmt"
	"strings"

	buildapi "github.com/openshift/api/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

type bundleSourceStep struct {
	config             api.BundleSourceStepConfiguration
	releaseBuildConfig *api.ReleaseBuildConfiguration
	resources          api.ResourceConfiguration
	buildClient        BuildClient
	imageClient        imageclientset.ImageStreamsGetter
	istClient          imageclientset.ImageStreamTagsGetter
	jobSpec            *api.JobSpec
	artifactDir        string
	pullSecret         *coreapi.Secret
}

func (s *bundleSourceStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *bundleSourceStep) Run(ctx context.Context) error {
	return results.ForReason("building_bundle_source").ForError(s.run(ctx))
}

func (s *bundleSourceStep) run(ctx context.Context) error {
	source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
	workingDir, err := getWorkingDir(s.istClient, source, s.jobSpec.Namespace())
	if err != nil {
		return fmt.Errorf("failed to get workingDir: %w", err)
	}
	dockerfile, err := s.bundleSourceDockerfile()
	if err != nil {
		return err
	}
	build := buildFromSource(
		s.jobSpec, api.PipelineImageStreamTagReferenceSource, api.BundleSourceName,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
			Images: []buildapi.ImageSource{
				{
					From: coreapi.ObjectReference{
						Kind: "ImageStreamTag",
						Name: source,
					},
					Paths: []buildapi.ImageSourcePath{{
						SourcePath:     fmt.Sprintf("%s/.", workingDir),
						DestinationDir: ".",
					}},
				},
			},
		},
		"",
		s.resources,
		s.pullSecret,
	)
	return handleBuild(ctx, s.buildClient, build, s.artifactDir)
}

func replaceCommand(pullSpec, with string) string {
	sedSub := fmt.Sprintf("s?%s?%s?g", pullSpec, with)
	return fmt.Sprintf(`find . -type f -regex ".*\.\(yaml\|yml\)" -exec sed -i %s {} +`, sedSub)
}

func (s *bundleSourceStep) bundleSourceDockerfile() (string, error) {
	var dockerCommands []string
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource))
	for _, sub := range s.config.Substitutions {
		streamName, tagName, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: sub.With})
		replaceSpec, err := utils.ImageDigestFor(s.imageClient, s.jobSpec.Namespace, streamName, tagName)()
		if err != nil {
			return "", fmt.Errorf("failed to get image digest for %s: %w", sub.With, err)
		}
		dockerCommands = append(dockerCommands, fmt.Sprintf(`RUN %s`, replaceCommand(sub.PullSpec, replaceSpec)))
	}
	return strings.Join(dockerCommands, "\n"), nil
}

func (s *bundleSourceStep) Requires() []api.StepLink {
	links := []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)}
	for _, sub := range s.config.Substitutions {
		imageStream, name, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: sub.With})
		links = append(links, api.LinkForImage(imageStream, name))
	}
	return links
}

func (s *bundleSourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(api.BundleSourceName)}
}

func (s *bundleSourceStep) Provides() api.ParameterMap {
	return api.ParameterMap{}
}

func (s *bundleSourceStep) Name() string { return api.BundleSourceName }

func (s *bundleSourceStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", api.BundleSourceName)
}

func BundleSourceStep(config api.BundleSourceStepConfiguration, releaseBuildConfig *api.ReleaseBuildConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageStreamsGetter, istClient imageclientset.ImageStreamTagsGetter, artifactDir string, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &bundleSourceStep{
		config:             config,
		releaseBuildConfig: releaseBuildConfig,
		resources:          resources,
		buildClient:        buildClient,
		imageClient:        imageClient,
		istClient:          istClient,
		artifactDir:        artifactDir,
		jobSpec:            jobSpec,
		pullSecret:         pullSecret,
	}
}
