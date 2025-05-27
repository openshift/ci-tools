package steps

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

type indexGeneratorStep struct {
	config             api.IndexGeneratorStepConfiguration
	releaseBuildConfig *api.ReleaseBuildConfiguration
	resources          api.ResourceConfiguration
	client             BuildClient
	podClient          kubernetes.PodClient
	jobSpec            *api.JobSpec
	pullSecret         *coreapi.Secret
	architectures      sets.Set[string]
	metricsAgent       *metrics.MetricsAgent
}

const IndexDataDirectory = "/index-data"
const IndexDockerfileName = "index.Dockerfile"

func (s *indexGeneratorStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*indexGeneratorStep) Validate() error { return nil }

func databaseIndex(client ctrlruntimeclient.Client, name, namespace string) (bool, error) {
	ist := &imagev1.ImageStreamTag{}
	ctx := context.TODO()
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, ist); err != nil {
		return false, fmt.Errorf("could not fetch source ImageStreamTag: %w", err)
	}
	// At the moment, we support only amd64
	labels, err := helper.LabelsOnISTagImage(ctx, client, ist, api.ReleaseArchitectureAMD64)
	if err != nil {
		return false, fmt.Errorf("failed to get value of the image label: %w", err)
	}
	if labels == nil {
		return false, nil
	}
	_, ok := labels["operators.operatorframework.io.index.database.v1"]
	return ok, nil
}

func (s *indexGeneratorStep) Run(ctx context.Context) error {
	return results.ForReason("building_index_generator").ForError(s.run(ctx))
}

func (s *indexGeneratorStep) run(ctx context.Context) error {
	logrus.Warn("DEPRECATION WARNING: Building index images is deprecated and will be removed from ci-operator soon. See https://docs.ci.openshift.org/docs/how-tos/testing-operator-sdk-operators/#moving-to-file-based-catalog for details.")
	source := fmt.Sprintf("%s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource)
	workingDir, err := getWorkingDir(s.client, source, s.jobSpec.Namespace())
	if err != nil {
		return fmt.Errorf("failed to get workingDir: %w", err)
	}
	if s.config.BaseIndex != "" {
		source := fmt.Sprintf("%s:%s", api.PipelineImageStream, s.config.BaseIndex)
		ok, err := databaseIndex(s.client, source, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to determine if the image %s/%s is sqlite based index: %w", s.jobSpec.Namespace(), source, err)
		}
		if !ok {
			logrus.Warn("Skipped building the index image: opm index commands, which are used by the ci-operator, interact only with a database index, but the base index is not one. Please refer to the FBC docs here: https://olm.operatorframework.io/docs/reference/file-based-catalogs/.")
			return nil
		} else {
			logrus.Debug("The base index image is sqlite based")
		}
	}
	dockerfile, err := s.indexGenDockerfile()
	if err != nil {
		return err
	}
	fromTag := api.PipelineImageStreamTagReferenceSource
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, fromTag, s.jobSpec)
	if err != nil {
		return err
	}
	var secrets []buildapi.SecretBuildSource
	if s.pullSecret != nil {
		secrets = append(secrets, buildapi.SecretBuildSource{
			Secret: coreapi.LocalObjectReference{Name: s.pullSecret.Name},
		})
	}
	build := buildFromSource(
		s.jobSpec, fromTag, s.config.To,
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
			Secrets: secrets,
		},
		fromDigest,
		"",
		s.resources,
		s.pullSecret,
		nil,
		"",
	)
	err = handleBuilds(ctx, s.client, s.podClient, *build, s.metricsAgent, newImageBuildOptions(s.architectures.UnsortedList()))
	if err != nil && strings.Contains(err.Error(), "error checking provided apis") {
		return results.ForReason("generating_index").WithError(err).Errorf("failed to generate operator index due to invalid bundle info: %v", err)
	}
	return err
}

func (s *indexGeneratorStep) indexGenDockerfile() (string, error) {
	var dockerCommands []string
	dockerCommands = append(dockerCommands, "FROM quay.io/operator-framework/upstream-opm-builder AS builder")
	if s.pullSecret != nil {
		dockerCommands = append(dockerCommands, "COPY .dockerconfigjson .")
		dockerCommands = append(dockerCommands, "RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json")
	}
	var bundles []string
	for _, bundleName := range s.config.OperatorIndex {
		fullSpec, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, bundleName)()
		if err != nil {
			return "", fmt.Errorf("failed to get image digest for bundle `%s`: %w", bundleName, err)
		}
		bundles = append(bundles, fullSpec)
	}
	baseIndex := ""
	if s.config.BaseIndex != "" {
		fullSpec, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, s.config.BaseIndex)()
		if err != nil {
			return "", fmt.Errorf("failed to get image digest for bundle `%s`: %w", s.config.BaseIndex, err)
		}
		baseIndex = fullSpec
	}
	opmCommand := fmt.Sprintf(`RUN ["opm", "index", "add", "--mode", "%s", "--bundles", "%s", "--out-dockerfile", "%s", "--generate"`, s.config.UpdateGraph, strings.Join(bundles, ","), IndexDockerfileName)
	if baseIndex != "" {
		opmCommand = fmt.Sprintf(`%s, "--from-index", "%s"`, opmCommand, baseIndex)
	}
	opmCommand = fmt.Sprintf("%s]", opmCommand)
	dockerCommands = append(dockerCommands, opmCommand)
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, api.PipelineImageStreamTagReferenceSource))
	dockerCommands = append(dockerCommands, fmt.Sprintf("WORKDIR %s", IndexDataDirectory))
	dockerCommands = append(dockerCommands, fmt.Sprintf("COPY --from=builder %s %s", IndexDockerfileName, IndexDockerfileName))
	dockerCommands = append(dockerCommands, "COPY --from=builder /database/ database")
	return strings.Join(dockerCommands, "\n"), nil
}

func (s *indexGeneratorStep) Requires() []api.StepLink {
	var links []api.StepLink
	for _, bundle := range s.config.OperatorIndex {
		imageStream, name, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: bundle}, nil)
		links = append(links, api.LinkForImage(imageStream, name))
	}
	if s.config.BaseIndex != "" {
		imageStream, name, _ := s.releaseBuildConfig.DependencyParts(api.StepDependency{Name: s.config.BaseIndex}, nil)
		links = append(links, api.LinkForImage(imageStream, name))
	}
	return links
}

func (s *indexGeneratorStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *indexGeneratorStep) Provides() api.ParameterMap {
	return api.ParameterMap{}
}

func (s *indexGeneratorStep) Name() string { return s.config.TargetName() }

func (s *indexGeneratorStep) Description() string {
	return fmt.Sprintf("Build image %s from the repository", s.config.To)
}

func (s *indexGeneratorStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *indexGeneratorStep) ResolveMultiArch() sets.Set[string] {
	return s.architectures
}

func (s *indexGeneratorStep) AddArchitectures(archs []string) {
	s.architectures.Insert(archs...)
}

func IndexGeneratorStep(
	config api.IndexGeneratorStepConfiguration,
	releaseBuildConfig *api.ReleaseBuildConfiguration,
	resources api.ResourceConfiguration,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	pullSecret *coreapi.Secret,
	metricsAgent *metrics.MetricsAgent,
) api.Step {
	return &indexGeneratorStep{
		config:             config,
		releaseBuildConfig: releaseBuildConfig,
		resources:          resources,
		client:             buildClient,
		podClient:          podClient,
		jobSpec:            jobSpec,
		pullSecret:         pullSecret,
		architectures:      sets.New[string](),
		metricsAgent:       metricsAgent,
	}
}
