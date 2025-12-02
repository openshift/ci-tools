package steps

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

const (
	SkippedImagesEnvVar = "SKIPPED_IMAGES"
)

func rawCommandDockerfile(from api.PipelineImageStreamTagReference, commands string) string {
	return fmt.Sprintf(`FROM %s:%s
RUN ["/bin/bash", "-c", %s]`, api.PipelineImageStream, from, strconv.Quote(fmt.Sprintf("set -o errexit; umask 0002; %s", commands)))
}

type pipelineImageCacheStep struct {
	config        api.PipelineImageCacheStepConfiguration
	resources     api.ResourceConfiguration
	client        BuildClient
	podClient     kubernetes.PodClient
	jobSpec       *api.JobSpec
	pullSecret    *coreapi.Secret
	architectures sets.Set[string]
	metricsAgent  *metrics.MetricsAgent
	skippedImages sets.Set[string]
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
	build := buildFromSource(
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
		s.config.Ref,
	)

	// Here we inject the SKIPPED_IMAGES environment variable to be utilized by the build command.
	if len(s.skippedImages) > 0 {
		build.Spec.Strategy.DockerStrategy.Env = append(build.Spec.Strategy.DockerStrategy.Env, coreapi.EnvVar{Name: SkippedImagesEnvVar, Value: strings.Join(sets.List(s.skippedImages), ",")})
	}

	return handleBuilds(ctx, s.client, s.podClient, *build, s.metricsAgent, newImageBuildOptions(s.architectures.UnsortedList()))
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

func (s *pipelineImageCacheStep) ResolveMultiArch() sets.Set[string] {
	return s.architectures
}

func (s *pipelineImageCacheStep) AddArchitectures(archs []string) {
	s.architectures.Insert(archs...)
}

func PipelineImageCacheStep(
	config api.PipelineImageCacheStepConfiguration,
	resources api.ResourceConfiguration,
	client BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	pullSecret *coreapi.Secret,
	metricsAgent *metrics.MetricsAgent,
	skippedImages sets.Set[string],
) api.Step {
	return &pipelineImageCacheStep{
		config:        config,
		resources:     resources,
		client:        client,
		podClient:     podClient,
		jobSpec:       jobSpec,
		pullSecret:    pullSecret,
		architectures: sets.New[string](),
		metricsAgent:  metricsAgent,
		skippedImages: skippedImages,
	}
}
