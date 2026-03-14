package steps

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/decorate"

	buildapi "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// srcAssemblyStep combines the architecture-independent "scratch-source"
// image (cloned source) with the architecture-specific build root to
// produce a "src" image for each required architecture.
type srcAssemblyStep struct {
	config        api.SrcAssemblyStepConfiguration
	resources     api.ResourceConfiguration
	client        BuildClient
	podClient     kubernetes.PodClient
	jobSpec       *api.JobSpec
	pullSecret    *corev1.Secret
	architectures sets.Set[string]
	metricsAgent  *metrics.MetricsAgent
}

func (s *srcAssemblyStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*srcAssemblyStep) Validate() error { return nil }

func (s *srcAssemblyStep) Run(ctx context.Context) error {
	return results.ForReason("assembling_src_image").ForError(s.run(ctx))
}

func srcAssemblyDockerfile(workingDir string) string {
	var commands []string
	commands = append(commands, "")
	commands = append(commands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceRoot))
	commands = append(commands, fmt.Sprintf("COPY --from=scratch-source %s/src %s/src", gopath, gopath))
	commands = append(commands, fmt.Sprintf("WORKDIR %s/", workingDir))
	commands = append(commands, fmt.Sprintf("ENV GOPATH=%s", gopath))
	commands = append(commands, "")
	return strings.Join(commands, "\n")
}

func (s *srcAssemblyStep) run(ctx context.Context) error {
	var refs []prowv1.Refs
	if s.jobSpec.Refs != nil {
		r := *s.jobSpec.Refs
		orgRepo := fmt.Sprintf("%s.%s", r.Org, r.Repo)
		if s.config.Ref == "" || orgRepo == s.config.Ref {
			refs = append(refs, r)
		}
	}
	for _, r := range s.jobSpec.ExtraRefs {
		orgRepo := fmt.Sprintf("%s.%s", r.Org, r.Repo)
		if s.config.Ref == "" || orgRepo == s.config.Ref {
			refs = append(refs, r)
		}
	}

	workingDir := decorate.DetermineWorkDir(gopath, refs)
	dockerfile := srcAssemblyDockerfile(workingDir)

	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, s.config.From, s.jobSpec)
	if err != nil {
		return err
	}

	build := buildFromSource(
		s.jobSpec, s.config.From, s.config.To,
		buildapi.BuildSource{
			Type:       buildapi.BuildSourceDockerfile,
			Dockerfile: &dockerfile,
			Images: []buildapi.ImageSource{
				{
					From: corev1.ObjectReference{
						Kind:      "ImageStreamTag",
						Namespace: s.jobSpec.Namespace(),
						Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.ScratchSourceFrom),
					},
					As: []string{"scratch-source"},
				},
			},
		},
		fromDigest,
		"",
		s.resources,
		s.pullSecret,
		nil,
		s.config.Ref,
	)

	return handleBuilds(ctx, s.client, s.podClient, *build, s.metricsAgent, newImageBuildOptions(s.architectures.UnsortedList()))
}

func (s *srcAssemblyStep) Requires() []api.StepLink {
	return []api.StepLink{
		api.InternalImageLink(s.config.From),
		api.InternalImageLink(s.config.ScratchSourceFrom),
	}
}

func (s *srcAssemblyStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *srcAssemblyStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *srcAssemblyStep) Name() string { return s.config.TargetName() }

func (s *srcAssemblyStep) Description() string {
	return fmt.Sprintf("Assemble source from %s onto %s and tag as %s", s.config.ScratchSourceFrom, s.config.From, s.config.To)
}

func (s *srcAssemblyStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *srcAssemblyStep) ResolveMultiArch() sets.Set[string] {
	return s.architectures
}

func (s *srcAssemblyStep) AddArchitectures(archs []string) {
	s.architectures.Insert(archs...)
}

func SrcAssemblyStep(
	config api.SrcAssemblyStepConfiguration,
	resources api.ResourceConfiguration,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	pullSecret *corev1.Secret,
	metricsAgent *metrics.MetricsAgent,
) api.Step {
	return &srcAssemblyStep{
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
