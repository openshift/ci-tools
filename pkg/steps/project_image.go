package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/api/image/docker10"
	"github.com/openshift/ci-tools/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type projectDirectoryImageBuildStep struct {
	config      api.ProjectDirectoryImageBuildStepConfiguration
	resources   api.ResourceConfiguration
	buildClient BuildClient
	imageClient imageclientset.ImageStreamsGetter
	istClient   imageclientset.ImageStreamTagsGetter
	jobSpec     *api.JobSpec
	artifactDir string
	dryLogger   *DryLogger
	pullSecret  *coreapi.Secret
}

func (s *projectDirectoryImageBuildStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *projectDirectoryImageBuildStep) Run(ctx context.Context, dry bool) error {
	source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)

	var workingDir string
	if dry {
		workingDir = "dry-fake"
	} else {
		ist, err := s.istClient.ImageStreamTags(s.jobSpec.Namespace).Get(source, meta.GetOptions{})
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
		workingDir = metadata.Config.WorkingDir
	}

	labels := make(map[string]string)
	// reset all labels that may be set by a lower level
	for _, key := range []string{
		"vcs-type",
		"vcs-ref",
		"vcs-url",
		"io.openshift.build.name",
		"io.openshift.build.namespace",
		"io.openshift.build.commit.id",
		"io.openshift.build.commit.ref",
		"io.openshift.build.commit.message",
		"io.openshift.build.commit.author",
		"io.openshift.build.commit.date",
		"io.openshift.build.source-location",
		"io.openshift.build.source-context-dir",
	} {
		labels[key] = ""
	}
	if refs := s.jobSpec.Refs; refs != nil {
		if len(refs.Pulls) == 0 {
			labels["vcs-type"] = "git"
			labels["vcs-ref"] = refs.BaseSHA
			labels["io.openshift.build.commit.id"] = refs.BaseSHA
			labels["io.openshift.build.commit.ref"] = refs.BaseRef
			labels["vcs-url"] = fmt.Sprintf("https://github.com/%s/%s", refs.Org, refs.Repo)
			labels["io.openshift.build.source-location"] = labels["vcs-url"]
			labels["io.openshift.build.source-context-dir"] = s.config.ContextDir
		}
		// TODO: we should consider setting enough info for a caller to reconstruct pulls to support
		// oc adm release info tooling
	}

	images := buildInputsFromStep(s.config.Inputs)
	if _, ok := s.config.Inputs["src"]; !ok {
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
			Type:   buildapi.BuildSourceImage,
			Images: images,
		},
		s.config.DockerfilePath,
		s.resources,
		s.pullSecret,
	)
	for k, v := range labels {
		build.Spec.Output.ImageLabels = append(build.Spec.Output.ImageLabels, buildapi.ImageLabel{
			Name:  k,
			Value: v,
		})
	}
	return handleBuild(ctx, s.buildClient, build, dry, s.artifactDir, s.dryLogger)
}

func (s *projectDirectoryImageBuildStep) Requires() []api.StepLink {
	links := []api.StepLink{
		api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
	}
	if len(s.config.From) > 0 {
		links = append(links, api.InternalImageLink(s.config.From))
	}
	for name := range s.config.Inputs {
		links = append(links, api.InternalImageLink(api.PipelineImageStreamTagReference(name)))
	}
	return links
}

func (s *projectDirectoryImageBuildStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *projectDirectoryImageBuildStep) Provides() (api.ParameterMap, api.StepLink) {
	if len(s.config.To) == 0 {
		return nil, nil
	}
	return api.ParameterMap{
		fmt.Sprintf("LOCAL_IMAGE_%s", strings.ToUpper(strings.Replace(string(s.config.To), "-", "_", -1))): func() (string, error) {
			is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(api.PipelineImageStream, meta.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("could not retrieve output imagestream: %v", err)
			}
			var registry string
			if len(is.Status.PublicDockerImageRepository) > 0 {
				registry = is.Status.PublicDockerImageRepository
			} else if len(is.Status.DockerImageRepository) > 0 {
				registry = is.Status.DockerImageRepository
			} else {
				return "", fmt.Errorf("image stream %s has no accessible image registry value", s.config.To)
			}
			return fmt.Sprintf("%s:%s", registry, s.config.To), nil
		},
	}, api.InternalImageLink(s.config.To)
}

func (s *projectDirectoryImageBuildStep) Name() string { return string(s.config.To) }

func (s *projectDirectoryImageBuildStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", s.config.To)
}

func ProjectDirectoryImageBuildStep(config api.ProjectDirectoryImageBuildStepConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageStreamsGetter, istClient imageclientset.ImageStreamTagsGetter, artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger, pullSecret *coreapi.Secret) api.Step {
	return &projectDirectoryImageBuildStep{
		config:      config,
		resources:   resources,
		buildClient: buildClient,
		imageClient: imageClient,
		istClient:   istClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		dryLogger:   dryLogger,
		pullSecret:  pullSecret,
	}
}
