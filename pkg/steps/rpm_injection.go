package steps

import (
	"context"
	"fmt"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
)

func rpmInjectionDockerfile(from api.PipelineImageStreamTagReference, repo string) string {
	return fmt.Sprintf(`FROM %s:%s
RUN echo $'[built]\nname = Built RPMs\nbaseurl = http://%s/\ngpgcheck = 0\nenabled = 0\n\n[origin-local-release]\nname = Built RPMs\nbaseurl = http://%s/\ngpgcheck = 0\nenabled = 0' > /etc/yum.repos.d/built.repo`, api.PipelineImageStream, from, repo, repo)
}

type rpmImageInjectionStep struct {
	config        api.RPMImageInjectionStepConfiguration
	resources     api.ResourceConfiguration
	client        BuildClient
	podClient     kubernetes.PodClient
	jobSpec       *api.JobSpec
	pullSecret    *coreapi.Secret
	architectures sets.Set[string]
	metricsAgent  *metrics.MetricsAgent
}

func (s *rpmImageInjectionStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*rpmImageInjectionStep) Validate() error { return nil }

func (s *rpmImageInjectionStep) Run(ctx context.Context) error {
	return results.ForReason("injecting_rpms").ForError(s.run(ctx))
}

func (s *rpmImageInjectionStep) run(ctx context.Context) error {
	route := &routev1.Route{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: RPMRepoName}, route); err != nil {
		return fmt.Errorf("could not get Route for RPM server: %w", err)
	}

	dockerfile := rpmInjectionDockerfile(s.config.From, route.Spec.Host)
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, s.config.From, s.jobSpec)
	if err != nil {
		return err
	}
	return handleBuilds(ctx, s.client, s.podClient, *buildFromSource(
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
		"",
	), s.metricsAgent, newImageBuildOptions(s.architectures.UnsortedList()))
}

func (s *rpmImageInjectionStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From), api.RPMRepoLink()}
}

func (s *rpmImageInjectionStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *rpmImageInjectionStep) Provides() api.ParameterMap {
	return nil
}

func (s *rpmImageInjectionStep) Name() string { return s.config.TargetName() }

func (s *rpmImageInjectionStep) Description() string {
	return "Inject an RPM repository that will point at the RPM server"
}

func (s *rpmImageInjectionStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *rpmImageInjectionStep) ResolveMultiArch() sets.Set[string] {
	return s.architectures
}

func (s *rpmImageInjectionStep) AddArchitectures(archs []string) {
	s.architectures.Insert(archs...)
}

func RPMImageInjectionStep(
	config api.RPMImageInjectionStepConfiguration,
	resources api.ResourceConfiguration,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	pullSecret *coreapi.Secret,
	metricsAgent *metrics.MetricsAgent,
) api.Step {
	return &rpmImageInjectionStep{
		config:        config,
		resources:     resources,
		client:        buildClient,
		podClient:     podClient,
		jobSpec:       jobSpec,
		pullSecret:    pullSecret,
		architectures: sets.New[string](),
		metricsAgent:  metricsAgent,
	}
}
