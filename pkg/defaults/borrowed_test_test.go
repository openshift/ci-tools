package defaults

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/validation"
)

type borrowedConfigs map[api.Metadata]api.ReleaseBuildConfiguration

func (configs borrowedConfigs) GetMatchingConfig(metadata api.Metadata) (api.ReleaseBuildConfiguration, error) {
	config, exists := configs[metadata]
	if !exists {
		return api.ReleaseBuildConfiguration{}, fmt.Errorf("no config for %s", metadata.AsString())
	}
	return *config.DeepCopy(), nil
}

type literalResolver struct{}

var borrowedResolverMetrics = metrics.NewMetrics("borrowed")

func (literalResolver) ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
	return config, nil
}

func TestBorrowedTestBuildGraph(t *testing.T) {
	t.Parallel()
	source := api.Metadata{Org: "openshift", Repo: "cluster-api-actuator-pkg", Branch: "release-5.0", Variant: "periodics"}
	component := api.Metadata{Org: "openshift", Repo: "installer", Branch: "main"}
	root := api.ImageStreamTagReference{Namespace: "ci", Name: "builder", Tag: "latest"}
	configs := borrowedConfigs{
		source: {
			Metadata: source,
			InputConfiguration: api.InputConfiguration{
				BuildRootImage: &api.BuildRootImageConfiguration{FromRepository: true},
				BaseImages:     map[string]api.ImageStreamTagReference{"tools": {Namespace: "ci", Name: "borrowed-tools", Tag: "latest"}},
				Releases:       map[string]api.UnresolvedRelease{api.LatestReleaseName: {Integration: &api.Integration{Namespace: "ocp", Name: "borrowed-release"}}},
			},
			Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{{To: "test-image", ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile.ci", Inputs: map[string]api.ImageBuildInputs{"tools": {As: []string{"builder"}}}}}}},
			Tests: []api.TestStepConfiguration{
				{As: "e2e-gcp-operator-periodic", MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{Test: []api.LiteralTestStep{{As: "test", From: "src", Commands: "JUNIT_DIR=${ARTIFACT_DIR} make test-e2e-periodic"}, {As: "image", From: "test-image", Commands: "true"}}}},
				{As: "other", ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "src"}},
			},
		},
		component: {
			InputConfiguration: api.InputConfiguration{
				BuildRootImage: &api.BuildRootImageConfiguration{ImageStreamTagReference: &root},
				BaseImages:     map[string]api.ImageStreamTagReference{"tools": {Namespace: "ci", Name: "PR-tools", Tag: "latest"}},
			},
			Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{{To: "test-image", From: "src", ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{DockerfilePath: "Dockerfile.ci", Inputs: map[string]api.ImageBuildInputs{"tools": {As: []string{"builder"}}}}}}},
		},
	}
	for _, included := range []bool{false, true} {
		t.Run(fmt.Sprintf("source already included=%t", included), func(t *testing.T) {
			handler := server.ResolveAndMergeConfigsAndInjectTest(configs, literalResolver{}, borrowedResolverMetrics)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			base := component
			if included {
				base.Org += "," + source.Org
				base.Repo += "," + source.Repo
				base.Branch += "," + source.Branch
				base.Variant = "," + source.Variant
			}
			config, err := server.NewResolverClient(httpServer.URL).ConfigWithTest(&base, &api.MetadataWithTest{Metadata: source, Test: "e2e-gcp-operator-periodic"})
			if err != nil {
				t.Fatal(err)
			}
			if len(config.Tests) != 1 || !config.BuildRootImage.FromRepository || len(config.Images.Items) != 2 {
				t.Fatalf("borrowed build/test inputs lost: %#v", config)
			}
			if config.Images.Items[0].To != "test-image" || config.Images.Items[1].To != "test-image-openshift.installer" || config.Images.Items[1].From != "src-openshift.installer" {
				t.Fatalf("image collision/namespacing: %#v", config.Images)
			}
			if diff := cmp.Diff(configs[source].Releases, config.Releases); diff != "" {
				t.Errorf("borrowed releases changed: %s", diff)
			}
			if diff := cmp.Diff(configs[source].BaseImages["tools"], config.BaseImages["tools"]); diff != "" {
				t.Errorf("borrowed base image changed: %s", diff)
			}
			if diff := cmp.Diff(configs[component].BaseImages["tools"], config.BaseImages["tools-openshift.installer"]); diff != "" {
				t.Errorf("PR base image not namespaced: %s", diff)
			}
			if diff := cmp.Diff(configs[source].Images.Items[0].Inputs, config.Images.Items[0].Inputs); diff != "" {
				t.Errorf("borrowed build inputs changed: %s", diff)
			}
			expectedInputs := map[string]api.ImageBuildInputs{"tools-openshift.installer": configs[component].Images.Items[0].Inputs["tools"]}
			if diff := cmp.Diff(expectedInputs, config.Images.Items[1].Inputs); diff != "" {
				t.Errorf("PR build inputs not namespaced: %s", diff)
			}
			job := &api.JobSpec{JobSpec: downwardapi.JobSpec{
				Refs:      &prowapi.Refs{Org: component.Org, Repo: component.Repo, BaseRef: component.Branch, BaseSHA: "base", WorkDir: true, Pulls: []prowapi.Pull{{Number: 10978, SHA: "head"}}},
				ExtraRefs: []prowapi.Refs{{Org: source.Org, Repo: source.Repo, BaseRef: source.Branch}},
			}, Metadata: config.Metadata}
			if !included {
				job.ExtraRefs = append([]prowapi.Refs{*job.Refs}, job.ExtraRefs...)
				job.Refs = nil
			} else {
				job.ExtraRefs[0].Pulls = []prowapi.Pull{{Number: 1, SHA: "source-head"}}
			}
			before, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			graph := FromConfigStatic(config)
			var stableOutputs []api.OutputImageTagStepConfiguration
			var pipelineImages []api.PipelineImageStreamTagReference
			for _, raw := range graph.Steps {
				if output := raw.OutputImageTagStepConfiguration; output != nil && output.To.Tag == "test-image" {
					stableOutputs = append(stableOutputs, *output)
				}
				if image := raw.ProjectDirectoryImageBuildStepConfiguration; image != nil {
					pipelineImages = append(pipelineImages, image.To)
				}
			}
			if len(stableOutputs) != 1 || stableOutputs[0].To.Name != api.StableImageStream || stableOutputs[0].From != "test-image-openshift.installer" {
				t.Fatalf("PR must own the collided stable output: %#v", stableOutputs)
			}
			if diff := cmp.Diff([]api.PipelineImageStreamTagReference{"test-image", "test-image-openshift.installer"}, pipelineImages); diff != "" {
				t.Fatalf("required pipeline images were dropped: %s", diff)
			}
			readRoot := false
			readSourceDockerfile := false
			runtime, err := runtimeStepConfigsForBuild(config, job, func(path string) ([]byte, error) {
				if strings.HasSuffix(path, ".ci-operator.yaml") {
					expected := "/home/prow/go/src/github.com/openshift/cluster-api-actuator-pkg/.ci-operator.yaml"
					if path != expected {
						t.Errorf("build root read from wrong repo: %s", path)
					}
					readRoot = true
					return []byte("build_root_image:\n  namespace: ci\n  name: builder\n  tag: latest\n"), nil
				}
				if path == "/home/prow/go/src/github.com/openshift/cluster-api-actuator-pkg/Dockerfile.ci" {
					readSourceDockerfile = true
				}
				return []byte("FROM scratch"), nil
			}, graph.InputImages(), true)
			if err != nil {
				t.Fatal(err)
			}
			if !readRoot {
				t.Fatal("from_repository was not resolved")
			}
			if !readSourceDockerfile {
				t.Fatal("borrowed Dockerfile was not read from its checkout")
			}
			var buildSteps []api.Step
			buildSteps = append(buildSteps, steps.InputEnvironmentStep("release-input", nil, []api.StepLink{api.ReleaseImagesLink(api.LatestReleaseName)}))
			parameters := api.NewDeferredParameters(nil)
			cfg := &Config{CIConfig: config, JobSpec: job, params: parameters}
			imageConfigs := graph.InputImages()
			for _, raw := range append(graph.Steps, runtime...) {
				switch {
				case raw.SourceStepConfiguration != nil:
					if raw.SourceStepConfiguration.To == "src" && raw.SourceStepConfiguration.Ref != "openshift.cluster-api-actuator-pkg" {
						t.Fatal("src clones the wrong repository")
					}
					buildSteps = append(buildSteps, steps.SourceStep(*raw.SourceStepConfiguration, nil, nil, nil, job, nil, nil, nil))
				case raw.InputImageTagStepConfiguration != nil:
					buildSteps = append(buildSteps, steps.InputImageTagStep(raw.InputImageTagStepConfiguration, nil, job))
				case raw.ProjectDirectoryImageBuildStepConfiguration != nil:
					buildSteps = append(buildSteps, steps.ProjectDirectoryImageBuildStep(*raw.ProjectDirectoryImageBuildStepConfiguration, config, nil, nil, nil, job, nil, nil))
				case raw.OutputImageTagStepConfiguration != nil:
					buildSteps = append(buildSteps, steps.OutputImageTagStep(*raw.OutputImageTagStepConfiguration, nil, job))
				case raw.TestStepConfiguration != nil:
					buildSteps = append(buildSteps, stepForTest(cfg, make(inputImageSet), raw.TestStepConfiguration, &imageConfigs)...)
				}
			}
			if err := validation.IsValidGraphConfiguration(append(graph.Steps, runtime...)); err != nil {
				t.Fatal(err)
			}
			partial, err := api.BuildPartialGraph(buildSteps, []string{"e2e-gcp-operator-periodic"})
			if err != nil {
				t.Fatalf("borrowed test dependency resolution failed: %v", err)
			}
			if _, errors := partial.TopologicalSort(); len(errors) != 0 {
				t.Fatalf("unsortable graph: %v", errors)
			}
			payloadGraph, err := api.BuildPartialGraph(buildSteps, []string{"[output:stable:test-image]"})
			if err != nil {
				t.Fatal(err)
			}
			payloadSteps, sortErrors := payloadGraph.TopologicalSort()
			if len(sortErrors) != 0 {
				t.Fatalf("unsortable payload output: %v", sortErrors)
			}
			var payloadBuilds []string
			for _, node := range payloadSteps {
				if node.Step.Name() == "test-image" || node.Step.Name() == "test-image-openshift.installer" {
					payloadBuilds = append(payloadBuilds, node.Step.Name())
				}
			}
			if diff := cmp.Diff([]string{"test-image-openshift.installer"}, payloadBuilds); diff != "" {
				t.Fatalf("payload output depends on borrowed rather than PR build: %s", diff)
			}
			var withoutSource []api.Step
			for _, step := range buildSteps {
				isSource := false
				for _, link := range step.Creates() {
					if api.InternalImageLink("src").SatisfiedBy(link) {
						isSource = true
					}
				}
				if !isSource {
					withoutSource = append(withoutSource, step)
				}
			}
			missingSource, err := api.BuildPartialGraph(withoutSource, []string{"e2e-gcp-operator-periodic"})
			if err != nil {
				t.Fatal(err)
			}
			_, sortErrors = missingSource.TopologicalSort()
			if len(sortErrors) == 0 || !strings.Contains(errors.Join(sortErrors...).Error(), fmt.Sprintf("<%#v>", api.InternalImageLink("src"))) {
				t.Fatalf("negative control did not reproduce missing pipeline:src: %v", sortErrors)
			}
			t.Logf("negative control rejects missing pipeline:src: %v", sortErrors)
			after, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(before, after); diff != "" {
				t.Errorf("primary PR refs replaced: %s", diff)
			}
		})
	}
}

func TestInjectedSourceMustMatchBranch(t *testing.T) {
	t.Parallel()
	job := &api.JobSpec{JobSpec: downwardapi.JobSpec{ExtraRefs: []prowapi.Refs{{Org: "openshift", Repo: "cluster-api-actuator-pkg", BaseRef: "main", WorkDir: true}}}, Metadata: api.Metadata{Org: "openshift", Repo: "cluster-api-actuator-pkg", Branch: "release-5.0"}}
	if determinePrimaryRef(job, true) != nil {
		t.Fatal("selected the wrong branch for an injected test")
	}
}
