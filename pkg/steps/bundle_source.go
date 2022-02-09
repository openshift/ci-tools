package steps

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

type bundleSourceStep struct {
	config             api.BundleSourceStepConfiguration
	releaseBuildConfig *api.ReleaseBuildConfiguration
	resources          api.ResourceConfiguration
	client             BuildClient
	jobSpec            *api.JobSpec
	pullSecret         *coreapi.Secret
}

func (s *bundleSourceStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*bundleSourceStep) Validate() error { return nil }

func (s *bundleSourceStep) Run(ctx context.Context) error {
	return results.ForReason("building_bundle_source").ForError(s.run(ctx))
}

func (s *bundleSourceStep) run(ctx context.Context) error {
	source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
	workingDir, err := getWorkingDir(s.client, source, s.jobSpec.Namespace())
	if err != nil {
		return fmt.Errorf("failed to get workingDir: %w", err)
	}
	dockerfile, err := s.bundleSourceDockerfile()
	if err != nil {
		return err
	}
	fromTag := api.PipelineImageStreamTagReferenceSource
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, fromTag, s.jobSpec)
	if err != nil {
		return err
	}
	build := buildFromSource(
		s.jobSpec, fromTag, api.PipelineImageStreamTagReferenceBundleSource,
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
		fromDigest,
		"",
		s.resources,
		s.pullSecret,
		nil,
	)
	return handleBuild(ctx, s.client, *build)
}

func replaceCommand(pullSpec, with string) string {
	sedSub := fmt.Sprintf("s?%s?%s?g", pullSpec, with)
	return fmt.Sprintf(`find . -type f -regex ".*\.\(yaml\|yml\)" -exec sed -i %s {} +`, sedSub)
}

func (s *bundleSourceStep) bundleSourceDockerfile() (string, error) {
	var dockerCommands []string
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource))
	for _, sub := range s.config.Substitutions {
		streamName, tagName, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: sub.With}, nil)
		replaceSpec, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, streamName, tagName)()
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
		imageStream, name, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: sub.With}, nil)
		if link := api.LinkForImage(imageStream, name); link != nil {
			links = append(links, link)
		} else {
			logrus.Warnf("Unable to resolve image '%s' to be substituted for '%s'.", sub.With, sub.PullSpec)
		}

	}
	return links
}

func (s *bundleSourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBundleSource)}
}

func (s *bundleSourceStep) Provides() api.ParameterMap {
	return api.ParameterMap{}
}

func (s *bundleSourceStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *bundleSourceStep) Name() string { return s.config.TargetName() }

func (s *bundleSourceStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", api.PipelineImageStreamTagReferenceBundleSource)
}

func BundleSourceStep(config api.BundleSourceStepConfiguration, releaseBuildConfig *api.ReleaseBuildConfiguration, resources api.ResourceConfiguration, client BuildClient, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &bundleSourceStep{
		config:             config,
		releaseBuildConfig: releaseBuildConfig,
		resources:          resources,
		client:             client,
		jobSpec:            jobSpec,
		pullSecret:         pullSecret,
	}
}
