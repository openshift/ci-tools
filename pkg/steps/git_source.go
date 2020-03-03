package steps

import (
	"context"
	"fmt"

	coreapi "k8s.io/api/core/v1"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"

	buildapi "github.com/openshift/api/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

type gitSourceStep struct {
	config          api.ProjectDirectoryImageBuildInputs
	resources       api.ResourceConfiguration
	imageClient     imageclientset.ImageV1Interface
	buildClient     BuildClient
	artifactDir     string
	jobSpec         *api.JobSpec
	dryLogger       *DryLogger
	cloneAuthConfig *CloneAuthConfig
	pullSecret      *coreapi.Secret
}

func (s *gitSourceStep) Inputs(dry bool) (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (s *gitSourceStep) Run(ctx context.Context, dry bool) error {
	if refs := determineRefsWorkdir(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		cloneURI := fmt.Sprintf("https://github.com/%s/%s.git", refs.Org, refs.Repo)
		var secretName string
		if s.cloneAuthConfig != nil {
			cloneURI = s.cloneAuthConfig.getCloneURI(refs.Org, refs.Repo)
			secretName = s.cloneAuthConfig.Secret.Name
		}

		return handleBuild(ctx, s.buildClient, buildFromSource(s.jobSpec, "", api.PipelineImageStreamTagReferenceRoot, buildapi.BuildSource{
			Type:         buildapi.BuildSourceGit,
			ContextDir:   s.config.ContextDir,
			SourceSecret: getSourceSecretFromName(secretName),
			Git: &buildapi.GitBuildSource{
				URI: cloneURI,
				Ref: refs.BaseRef,
			},
		}, s.config.DockerfilePath, s.resources, s.pullSecret), dry, s.artifactDir, s.dryLogger)
	}

	return fmt.Errorf("nothing to build source image from, no refs")
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

func determineRefsWorkdir(refs *prowapi.Refs, extraRefs []prowapi.Refs) *prowapi.Refs {
	var totalRefs []prowapi.Refs

	if refs != nil {
		totalRefs = append(totalRefs, *refs)
	}
	totalRefs = append(totalRefs, extraRefs...)

	if len(totalRefs) == 0 {
		return nil
	}

	for _, ref := range totalRefs {
		if ref.WorkDir {
			return &ref
		}
	}

	return &totalRefs[0]
}

// GitSourceStep returns gitSourceStep that holds all the required information to create a build from a git source.
func GitSourceStep(config api.ProjectDirectoryImageBuildInputs, resources api.ResourceConfiguration, buildClient BuildClient, imageClient imageclientset.ImageV1Interface, artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger, cloneAuthConfig *CloneAuthConfig, pullSecret *coreapi.Secret) api.Step {
	return &gitSourceStep{
		config:          config,
		resources:       resources,
		buildClient:     buildClient,
		imageClient:     imageClient,
		artifactDir:     artifactDir,
		jobSpec:         jobSpec,
		dryLogger:       dryLogger,
		cloneAuthConfig: cloneAuthConfig,
		pullSecret:      pullSecret,
	}
}
