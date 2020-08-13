package steps

import (
	"context"
	"fmt"
	"strings"

	buildapi "github.com/openshift/api/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util"
)

type bundleSourceStep struct {
	config      api.BundleSourceStepConfiguration
	resources   api.ResourceConfiguration
	buildClient BuildClient
	imageClient imageclientset.ImageStreamsGetter
	istClient   imageclientset.ImageStreamTagsGetter
	jobSpec     *api.JobSpec
	artifactDir string
	pullSecret  *coreapi.Secret
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
	return fmt.Sprintf("find . -type f -name \\*.yaml -exec sed -i 's?%s?%s?g' {} +", pullSpec, with)
}

func (s *bundleSourceStep) bundleSourceDockerfile() (string, error) {
	var dockerCommands []string
	dockerCommands = append(dockerCommands, "")
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource))
	is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace()).Get(context.TODO(), api.StableImageStream, meta.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get stable imagestream: %w", err)
	}
	for _, sub := range s.config.Substitutions {
		replaceSpec, exists := util.ResolvePullSpec(is, sub.With, false)
		if !exists {
			return "", fmt.Errorf("failed to get replacement imagestream for image tag `%s`", sub.With)
		}
		// The \ character has to be escaped in the dockerfile to run correctly
		replace := strings.ReplaceAll(replaceCommand(sub.PullSpec, replaceSpec), `\`, `\\`)
		dockerCommands = append(dockerCommands, fmt.Sprintf(`RUN ["bash", "-c", "%s"]`, replace))
	}
	dockerCommands = append(dockerCommands, "")
	return strings.Join(dockerCommands, "\n"), nil
}

func (s *bundleSourceStep) Requires() []api.StepLink {
	return []api.StepLink{
		api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
	}
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

func BundleSourceStep(config api.BundleSourceStepConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageStreamsGetter, istClient imageclientset.ImageStreamTagsGetter, artifactDir string, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &bundleSourceStep{
		config:      config,
		resources:   resources,
		buildClient: buildClient,
		imageClient: imageClient,
		istClient:   istClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		pullSecret:  pullSecret,
	}
}
