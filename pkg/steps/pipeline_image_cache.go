package steps

import (
	"context"
	"fmt"
	"strconv"

	coreapi "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

func rawCommandDockerfile(from api.PipelineImageStreamTagReference, commands string) string {
	return fmt.Sprintf(`FROM %s:%s
RUN ["/bin/bash", "-c", %s]`, api.PipelineImageStream, from, strconv.Quote(fmt.Sprintf("set -o errexit; umask 0002; %s", commands)))
}

type pipelineImageCacheStep struct {
	config     api.PipelineImageCacheStepConfiguration
	resources  api.ResourceConfiguration
	client     BuildClient
	jobSpec    *api.JobSpec
	pullSecret *coreapi.Secret
}

func (s *pipelineImageCacheStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*pipelineImageCacheStep) Validate() error { return nil }

func (s *pipelineImageCacheStep) Run(ctx context.Context) error {
	return results.ForReason("building_cache_image").ForError(s.run(ctx))
}

func (s *pipelineImageCacheStep) run(ctx context.Context) error {
	dockerfile := rawCommandDockerfile(s.config.From, s.config.Commands)
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, s.config.From, s.jobSpec)
	if err != nil {
		return err
	}
	return handleBuild(ctx, s.client, *buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
		},
		fromDigest,
		"",
		s.resources,
		s.pullSecret,
		nil,
	))
}

func (s *pipelineImageCacheStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *pipelineImageCacheStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *pipelineImageCacheStep) Provides() api.ParameterMap {
	if len(s.config.To) == 0 {
		return nil
	}
	return api.ParameterMap{
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *pipelineImageCacheStep) Name() string { return s.config.TargetName() }

func (s *pipelineImageCacheStep) Description() string {
	return fmt.Sprintf("Store build results into a layer on top of %s and save as %s", s.config.From, s.config.To)
}

func (s *pipelineImageCacheStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func PipelineImageCacheStep(config api.PipelineImageCacheStepConfiguration, resources api.ResourceConfiguration, client BuildClient, jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &pipelineImageCacheStep{
		config:     config,
		resources:  resources,
		client:     client,
		jobSpec:    jobSpec,
		pullSecret: pullSecret,
	}
}
