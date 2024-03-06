package steps

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

type fakeStep struct {
	name      string
	runErr    error
	shouldRun bool
	requires  []api.StepLink
	creates   []api.StepLink

	lock    sync.Mutex
	numRuns int
}

func (*fakeStep) Inputs() (api.InputDefinition, error) { return nil, nil }
func (*fakeStep) Validate() error                      { return nil }

func (f *fakeStep) Run(ctx context.Context, o *api.RunOptions) error {
	defer f.lock.Unlock()
	f.lock.Lock()
	f.numRuns = f.numRuns + 1

	return f.runErr
}
func (f *fakeStep) Requires() []api.StepLink          { return f.requires }
func (f *fakeStep) Creates() []api.StepLink           { return f.creates }
func (f *fakeStep) Name() string                      { return f.name }
func (f *fakeStep) Description() string               { return f.name }
func (*fakeStep) Objects() []ctrlruntimeclient.Object { return nil }

func (f *fakeStep) Provides() api.ParameterMap { return nil }

func TestStepsRun(t *testing.T) {
	testCases := []struct {
		id          string
		steps       []*fakeStep
		errExpected []error
		cancelled   bool
	}{
		{
			id: "happy case, no errors",
			steps: []*fakeStep{
				{
					name:      "root",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
				}, {
					name:      "other",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "other"})},
					creates:   []api.StepLink{api.InternalImageLink("other")},
				}, {
					name:      "src",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
				}, {
					name:      "bin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
				}, {
					name:      "testBin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceTestBinaries)},
				}, {
					name:      "rpm",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
				}, {
					name:      "unrelated",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink("other"), api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
					creates:   []api.StepLink{api.InternalImageLink("unrelated")},
				}, {
					name:      "final",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink("unrelated")},
					creates:   []api.StepLink{api.InternalImageLink("final")},
				},
			},
		},
		{
			id: "sad case with one error",
			steps: []*fakeStep{
				{
					name:      "root",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
				},
				{
					name:      "other",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "other"})},
					creates:   []api.StepLink{api.InternalImageLink("other")},
				},
				{
					name:      "src",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
				},
				{
					name:      "bin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
				},
				{
					name:      "testBin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceTestBinaries)},
				},
				{
					name:      "rpm",
					runErr:    errors.New("oopsie"),
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
				},
				{
					name:      "unrelated",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("other"), api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
					creates:   []api.StepLink{api.InternalImageLink("unrelated")},
				}, {
					name:      "final",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("unrelated")},
					creates:   []api.StepLink{api.InternalImageLink("final")},
				},
			},
			errExpected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step rpm failed: oopsie"),
			},
		},
		{
			id: "execution cancelled, expect one error",
			steps: []*fakeStep{
				{
					name:      "root",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
				},
				{
					name:      "other",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "other"})},
					creates:   []api.StepLink{api.InternalImageLink("other")},
				},
				{
					name:      "src",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
				},
				{
					name:      "bin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
				},
				{
					name:      "testBin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceTestBinaries)},
				},
				{
					name:      "rpm",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
				},
				{
					name:      "unrelated",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("other"), api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
					creates:   []api.StepLink{api.InternalImageLink("unrelated")},
				}, {
					name:      "final",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("unrelated")},
					creates:   []api.StepLink{api.InternalImageLink("final")},
				},
			},
			errExpected: []error{
				results.ForReason("interrupted").ForError(errors.New("execution cancelled")),
			},
			cancelled: true,
		},
		{
			id: "execution cancelled but a step failed as well, expect multiple errors",
			steps: []*fakeStep{
				{
					name:      "root",
					runErr:    errors.New("oopsie"),
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
				},
				{
					name:      "other",
					shouldRun: true,
					requires:  []api.StepLink{api.ExternalImageLink(api.ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "other"})},
					creates:   []api.StepLink{api.InternalImageLink("other")},
				},
				{
					name:      "src",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRoot)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
				},
				{
					name:     "bin",
					requires: []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
				},
				{
					name:      "testBin",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceSource)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceTestBinaries)},
				},
				{
					name:      "rpm",
					shouldRun: true,
					requires:  []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceBinaries)},
					creates:   []api.StepLink{api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
				},
				{
					name:      "unrelated",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("other"), api.InternalImageLink(api.PipelineImageStreamTagReferenceRPMs)},
					creates:   []api.StepLink{api.InternalImageLink("unrelated")},
				}, {
					name:      "final",
					shouldRun: false,
					requires:  []api.StepLink{api.InternalImageLink("unrelated")},
					creates:   []api.StepLink{api.InternalImageLink("final")},
				},
			},
			errExpected: []error{
				results.ForReason("interrupted").ForError(errors.New("execution cancelled")),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step root failed: oopsie"),
			},
			cancelled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			shouldFail := 0
			shouldRun := 0

			var steps []api.Step
			for _, step := range tc.steps {
				if step.runErr != nil {
					shouldFail++
				}
				if step.shouldRun {
					shouldRun++
				}
				steps = append(steps, step)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			suites, _, errs := Run(ctx, api.BuildGraph(steps), &api.RunOptions{})
			if errs == nil && len(tc.errExpected) > 0 {
				t.Error("got no error but expected one")
			}

			var expectedErrorsString []string
			for _, e := range tc.errExpected {
				s := e.Error()
				for _, r := range results.Reasons(e) {
					expectedErrorsString = append(expectedErrorsString, fmt.Sprintf("%s: %s", r, s))
				}
			}
			var actualErrorsString []string
			for _, e := range errs {
				s := e.Error()
				for _, r := range results.Reasons(e) {
					actualErrorsString = append(actualErrorsString, fmt.Sprintf("%s: %s", r, s))
				}
			}
			sort.Strings(expectedErrorsString)
			sort.Strings(actualErrorsString)

			if !reflect.DeepEqual(expectedErrorsString, actualErrorsString) {
				t.Fatalf(cmp.Diff(expectedErrorsString, actualErrorsString))
			}

			if !tc.cancelled {
				if suites.Suites != nil && len(suites.Suites) != 1 ||
					len(suites.Suites[0].TestCases) != shouldRun ||
					int(suites.Suites[0].NumTests) != shouldRun ||
					int(suites.Suites[0].NumFailed) != shouldFail {
					t.Errorf("unexpected junit output: %#v", suites.Suites[0])
				}

				for _, step := range tc.steps {
					if step.shouldRun && step.numRuns != 1 {
						t.Errorf("step %s did not run just once, but %d times", step.name, step.numRuns)
					}
					if !step.shouldRun && step.numRuns != 0 {
						t.Errorf("step %s expected to never run, but ran %d times", step.name, step.numRuns)
					}
				}
			}
		})
	}
}
