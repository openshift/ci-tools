package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/api/image/docker10"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

type projectDirectoryImageBuildStep struct {
	config      api.ProjectDirectoryImageBuildStepConfiguration
	resources   api.ResourceConfiguration
	buildClient BuildClient
	imageClient imageclientset.ImageStreamsGetter
	istClient   imageclientset.ImageStreamTagsGetter
	jobSpec     *api.JobSpec
	artifactDir string
	pullSecret  *coreapi.Secret
}

func (s *projectDirectoryImageBuildStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *projectDirectoryImageBuildStep) Run(ctx context.Context) error {
	return results.ForReason("building_project_image").ForError(s.run(ctx))
}

func (s *projectDirectoryImageBuildStep) run(ctx context.Context) error {

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
	// If image being built is an operator bundle, use the bundle source instead of original source
	if strings.HasPrefix(string(s.config.To), api.BundlePrefix) {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.BundleSourceName)
		workingDir, err := getWorkingDir(s.istClient, source, s.jobSpec.Namespace())
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
	} else if s.config.To == api.IndexImageName {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.IndexImageGeneratorName)
		workingDir, err := getWorkingDir(s.istClient, source, s.jobSpec.Namespace())
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
		workingDir, err := getWorkingDir(s.istClient, source, s.jobSpec.Namespace())
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
	return handleBuild(ctx, s.buildClient, build, s.artifactDir)
}

func getWorkingDir(istClient imageclientset.ImageStreamTagsGetter, source, namespace string) (string, error) {
	ist, err := istClient.ImageStreamTags(namespace).Get(context.TODO(), source, meta.GetOptions{})
	if err != nil {
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
	if strings.HasPrefix(string(s.config.To), api.BundlePrefix) {
		links = append(links, api.InternalImageLink(api.BundleSourceName))
	}
	if s.config.To == api.IndexImageName {
		links = append(links, api.InternalImageLink(api.IndexImageGeneratorName))
	}
	for name := range s.config.Inputs {
		links = append(links, api.InternalImageLink(api.PipelineImageStreamTagReference(name)))
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
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.imageClient, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *projectDirectoryImageBuildStep) Name() string { return string(s.config.To) }

func (s *projectDirectoryImageBuildStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", s.config.To)
}

func ProjectDirectoryImageBuildStep(config api.ProjectDirectoryImageBuildStepConfiguration, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageStreamsGetter, istClient imageclientset.ImageStreamTagsGetter, artifactDir string, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &projectDirectoryImageBuildStep{
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
