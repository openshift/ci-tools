package steps

import (
	"context"
	"fmt"

	coreapi "k8s.io/api/core/v1"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

type gitSourceStep struct {
	config          api.ProjectDirectoryImageBuildInputs
	resources       api.ResourceConfiguration
	buildClient     BuildClient
	jobSpec         *api.JobSpec
	cloneAuthConfig *CloneAuthConfig
	pullSecret      *coreapi.Secret
}

func (s *gitSourceStep) Inputs() (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (*gitSourceStep) Validate() error { return nil }

func (s *gitSourceStep) Run(ctx context.Context) error {
	return results.ForReason("building_image_from_source").ForError(s.run(ctx))
}

func (s *gitSourceStep) run(ctx context.Context) error {
	if refs := determineRefsWorkdir(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		cloneURI := fmt.Sprintf("https://github.com/%s/%s.git", refs.Org, refs.Repo)
		var secretName string
		if s.cloneAuthConfig != nil {
			cloneURI = s.cloneAuthConfig.getCloneURI(refs.Org, refs.Repo)
			secretName = s.cloneAuthConfig.Secret.Name
		}

		return handleBuild(ctx, s.buildClient, *buildFromSource(s.jobSpec, "", api.PipelineImageStreamTagReferenceRoot, buildapi.BuildSource{
			Type:         buildapi.BuildSourceGit,
			Dockerfile:   s.config.DockerfileLiteral,
			ContextDir:   s.config.ContextDir,
			SourceSecret: getSourceSecretFromName(secretName),
			Git: &buildapi.GitBuildSource{
				URI: cloneURI,
				Ref: refs.BaseRef,
			},
		}, "", s.config.DockerfilePath, s.resources, s.pullSecret, nil))
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

func (s *gitSourceStep) Provides() api.ParameterMap {
	return nil
}

func (s *gitSourceStep) Objects() []ctrlruntimeclient.Object {
	return s.buildClient.Objects()
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
func GitSourceStep(config api.ProjectDirectoryImageBuildInputs, resources api.ResourceConfiguration, buildClient BuildClient, jobSpec *api.JobSpec, cloneAuthConfig *CloneAuthConfig, pullSecret *coreapi.Secret) api.Step {
	return &gitSourceStep{
		config:          config,
		resources:       resources,
		buildClient:     buildClient,
		jobSpec:         jobSpec,
		cloneAuthConfig: cloneAuthConfig,
		pullSecret:      pullSecret,
	}
}
