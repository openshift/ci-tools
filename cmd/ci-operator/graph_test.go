package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add imagev1 to scheme: %v", err))
	}
}

type fakeValidationStep struct {
	name string
	err  error
}

func (*fakeValidationStep) Inputs() (api.InputDefinition, error) { return nil, nil }
func (*fakeValidationStep) Run(ctx context.Context) error        { return nil }
func (*fakeValidationStep) Requires() []api.StepLink             { return nil }
func (*fakeValidationStep) Creates() []api.StepLink              { return nil }
func (f *fakeValidationStep) Name() string                       { return f.name }
func (*fakeValidationStep) Description() string                  { return "" }
func (*fakeValidationStep) Provides() api.ParameterMap           { return nil }
func (f *fakeValidationStep) Validate() error                    { return f.err }
func (*fakeValidationStep) Objects() []ctrlruntimeclient.Object  { return nil }

func (*fakeValidationStep) IsMultiArch() bool { return false }
func (*fakeValidationStep) SetMultiArch(bool) {}

func TestBuildPartialGraph(t *testing.T) {
	testCases := []struct {
		name           string
		input          []api.Step
		targetName     string
		expectedErrors []error
	}{
		{
			name: "Missing input image results in human-readable error",
			input: []api.Step{
				steps.InputImageTagStep(
					&api.InputImageTagStepConfiguration{InputImage: api.InputImage{To: api.PipelineImageStreamTagReferenceRoot}},
					loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Name: ":"}}).Build(), nil),
					nil,
				),
				steps.SourceStep(api.SourceStepConfiguration{From: api.PipelineImageStreamTagReferenceRoot, To: api.PipelineImageStreamTagReferenceSource}, api.ResourceConfiguration{}, nil, nil, &api.JobSpec{}, nil, nil, nil),
				steps.ProjectDirectoryImageBuildStep(
					api.ProjectDirectoryImageBuildStepConfiguration{
						From: api.PipelineImageStreamTagReferenceSource,
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							Inputs: map[string]api.ImageBuildInputs{"cli": {Paths: []api.ImageSourcePath{{DestinationDir: ".", SourcePath: "/usr/bin/oc"}}}},
						},
						To: api.PipelineImageStreamTagReference("oc-bin-image"),
					},
					&api.ReleaseBuildConfiguration{}, api.ResourceConfiguration{}, nil, nil, nil, nil, nil,
				),
				steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil),
				steps.ImagesReadyStep(steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil).Creates()),
			},
			targetName: "[images]",
			expectedErrors: []error{
				errors.New("steps are missing dependencies"),
				errors.New(`step [output::] is missing dependencies: <&api.internalImageStreamLink{name:"stable"}>, <&api.internalImageStreamTagLink{name:"pipeline", tag:"oc-bin-image", unsatisfiableError:""}>`),
				errors.New(`step oc-bin-image is missing dependencies: "cli" is neither an imported nor a built image`),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := api.BuildPartialGraph(tc.input, []string{tc.targetName})
			if err != nil {
				t.Fatalf("failed to build graph: %v", err)
			}

			_, errs := graph.TopologicalSort()
			testhelper.Diff(t, "errors", errs, tc.expectedErrors, testhelper.EquateErrorMessage)
		})
	}
}

func TestValidateSteps(t *testing.T) {
	valid0 := fakeValidationStep{name: "valid0"}
	valid1 := fakeValidationStep{name: "valid1"}
	valid2 := fakeValidationStep{name: "valid2"}
	valid3 := fakeValidationStep{name: "valid3"}
	invalid0 := fakeValidationStep{
		name: "invalid0",
		err:  errors.New("invalid0"),
	}
	for _, tc := range []struct {
		name     string
		expected bool
		steps    api.OrderedStepList
	}{{
		name:     "empty graph",
		expected: true,
	}, {
		name:     "valid graph",
		expected: true,
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &valid3,
		}},
	}, {
		name: "invalid graph",
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &invalid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSteps(tc.steps)
			if (err == nil) != tc.expected {
				t.Errorf("got %v, want %v", err == nil, tc.expected)
			}
		})
	}
}
