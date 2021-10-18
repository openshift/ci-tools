package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/gofuzz"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMatches(t *testing.T) {
	var testCases = []struct {
		name    string
		first   StepLink
		second  StepLink
		matches bool
	}{
		{
			name:    "internal matches itself",
			first:   InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			second:  InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			matches: true,
		},
		{
			name:    "external matches itself",
			first:   ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			second:  ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			matches: true,
		},
		{
			name:    "rpm matches itself",
			first:   RPMRepoLink(),
			second:  RPMRepoLink(),
			matches: true,
		},
		{
			name:    "release images matches itself",
			first:   ReleaseImagesLink(LatestReleaseName),
			second:  ReleaseImagesLink(LatestReleaseName),
			matches: true,
		},
		{
			name:    "different internal do not match",
			first:   InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			second:  InternalImageLink(PipelineImageStreamTagReferenceSource),
			matches: false,
		},
		{
			name:    "different external do not match",
			first:   ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			second:  ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "other"}),
			matches: false,
		},
		{
			name:    "internal does not match external",
			first:   InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			second:  ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			matches: false,
		},
		{
			name:    "internal does not match RPM",
			first:   InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			second:  RPMRepoLink(),
			matches: false,
		},
		{
			name:    "internal does not match release images",
			first:   InternalImageLink(PipelineImageStreamTagReferenceRPMs),
			second:  ReleaseImagesLink(LatestReleaseName),
			matches: false,
		},
		{
			name:    "external does not match RPM",
			first:   ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			second:  RPMRepoLink(),
			matches: false,
		},
		{
			name:    "external does not match release images",
			first:   ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "name", Tag: "latest"}),
			second:  ReleaseImagesLink(LatestReleaseName),
			matches: false,
		},
		{
			name:    "RPM does not match release images",
			first:   RPMRepoLink(),
			second:  ReleaseImagesLink(LatestReleaseName),
			matches: false,
		},
	}

	for _, testCase := range testCases {
		if actual, expected := testCase.first.SatisfiedBy(testCase.second), testCase.matches; actual != expected {
			message := "not match"
			if testCase.matches {
				message = "match"
			}
			t.Errorf("%s: expected links to %s, but they didn't:\nfirst:\n\t%v\nsecond:\n\t%v", testCase.name, message, testCase.first, testCase.second)
		}
	}
}

type fakeStep struct {
	requires []StepLink
	creates  []StepLink
	name     string
}

func (f *fakeStep) Inputs() (InputDefinition, error) { return nil, nil }
func (f *fakeStep) Validate() error                  { return nil }
func (f *fakeStep) Run(ctx context.Context) error    { return nil }

func (f *fakeStep) Requires() []StepLink                { return f.requires }
func (f *fakeStep) Creates() []StepLink                 { return f.creates }
func (f *fakeStep) Name() string                        { return f.name }
func (f *fakeStep) Description() string                 { return f.name }
func (f *fakeStep) Objects() []ctrlruntimeclient.Object { return nil }

func (f *fakeStep) Provides() ParameterMap { return nil }

func TestBuildGraph(t *testing.T) {
	root := &fakeStep{
		requires: []StepLink{ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceRoot)},
	}
	other := &fakeStep{
		requires: []StepLink{ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "other"})},
		creates:  []StepLink{InternalImageLink("other")},
	}
	src := &fakeStep{
		requires: []StepLink{InternalImageLink(PipelineImageStreamTagReferenceRoot)},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceSource)},
	}
	bin := &fakeStep{
		requires: []StepLink{InternalImageLink(PipelineImageStreamTagReferenceSource)},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceBinaries)},
	}
	testBin := &fakeStep{
		requires: []StepLink{InternalImageLink(PipelineImageStreamTagReferenceSource)},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceTestBinaries)},
	}
	rpm := &fakeStep{
		requires: []StepLink{InternalImageLink(PipelineImageStreamTagReferenceBinaries)},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceRPMs)},
	}
	unrelated := &fakeStep{
		requires: []StepLink{InternalImageLink("other"), InternalImageLink(PipelineImageStreamTagReferenceRPMs)},
		creates:  []StepLink{InternalImageLink("unrelated")},
	}
	final := &fakeStep{
		requires: []StepLink{InternalImageLink("unrelated")},
		creates:  []StepLink{InternalImageLink("final")},
	}

	duplicateRoot := &fakeStep{
		requires: []StepLink{ExternalImageLink(ImageStreamTagReference{Namespace: "ns", Name: "base", Tag: "latest"})},
		creates:  []StepLink{InternalImageLink(PipelineImageStreamTagReferenceRoot)},
	}
	duplicateSrc := &fakeStep{
		requires: []StepLink{
			InternalImageLink(PipelineImageStreamTagReferenceRoot),
			InternalImageLink(PipelineImageStreamTagReferenceRoot),
		},
		creates: []StepLink{InternalImageLink("other")},
	}

	var testCases = []struct {
		name   string
		input  []Step
		output StepGraph
	}{
		{
			name:  "basic graph",
			input: []Step{root, other, src, bin, testBin, rpm, unrelated, final},
			output: StepGraph{{
				Step: root,
				Children: []*StepNode{{
					Step: src,
					Children: []*StepNode{{
						Step: bin,
						Children: []*StepNode{{
							Step: rpm,
							Children: []*StepNode{{
								Step: unrelated,
								Children: []*StepNode{{
									Step:     final,
									Children: []*StepNode{},
								}},
							}},
						}},
					}, {
						Step:     testBin,
						Children: []*StepNode{},
					}},
				}},
			}, {
				Step: other,
				Children: []*StepNode{{
					Step: unrelated,
					Children: []*StepNode{{
						Step:     final,
						Children: []*StepNode{},
					}},
				}},
			}},
		},
		{
			name:  "duplicate links",
			input: []Step{duplicateRoot, duplicateSrc},
			output: StepGraph{{
				Step: duplicateRoot,
				Children: []*StepNode{{
					Step:     duplicateSrc,
					Children: []*StepNode{},
				}},
			}},
		},
	}

	for _, testCase := range testCases {
		if actual, expected := BuildGraph(testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s: did not generate step graph as expected:\nwant:\n\t%v\nhave:\n\t%v", testCase.name, expected, actual)
		}
	}
}

type fakeValidationStep struct {
	name string
	err  error
}

func (*fakeValidationStep) Inputs() (InputDefinition, error)    { return nil, nil }
func (*fakeValidationStep) Run(ctx context.Context) error       { return nil }
func (*fakeValidationStep) Requires() []StepLink                { return nil }
func (*fakeValidationStep) Creates() []StepLink                 { return nil }
func (f *fakeValidationStep) Name() string                      { return f.name }
func (*fakeValidationStep) Description() string                 { return "" }
func (*fakeValidationStep) Provides() ParameterMap              { return nil }
func (f *fakeValidationStep) Validate() error                   { return f.err }
func (*fakeValidationStep) Objects() []ctrlruntimeclient.Object { return nil }

func TestValidateGraph(t *testing.T) {
	valid0 := fakeValidationStep{name: "valid0"}
	valid1 := fakeValidationStep{name: "valid1"}
	valid2 := fakeValidationStep{name: "valid2"}
	valid3 := fakeValidationStep{name: "valid3"}
	invalid0 := fakeValidationStep{
		name: "invalid0",
		err:  errors.New("invalid0"),
	}
	invalid1 := fakeValidationStep{
		name: "invalid1",
		err:  errors.New("invalid0"),
	}
	for _, tc := range []struct {
		name     string
		expected bool
		graph    StepGraph
	}{{
		name:     "empty graph",
		expected: true,
	}, {
		name:     "valid graph",
		expected: true,
		graph: StepGraph{{
			Step: &valid0,
			Children: []*StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &valid3,
		}},
	}, {
		name:     "valid graph, duplicate steps",
		expected: true,
		graph: StepGraph{{
			Step: &valid0,
			Children: []*StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &valid3,
			Children: []*StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}},
	}, {
		name: "invalid graph",
		graph: StepGraph{{
			Step: &valid0,
			Children: []*StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &invalid0,
			Children: []*StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}},
	}, {
		name: "invalid graph, duplicate steps",
		graph: StepGraph{{
			Step: &valid0,
			Children: []*StepNode{
				{Step: &invalid0},
				{Step: &invalid1},
			},
		}, {
			Step: &valid3,
			Children: []*StepNode{
				{Step: &invalid0},
				{Step: &invalid1},
			},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.graph.Validate()
			if (err == nil) != tc.expected {
				t.Errorf("got %v, want %v", err == nil, tc.expected)
			}
			msgs := sets.NewString()
			for _, e := range err {
				msg := e.Error()
				if msgs.Has(msg) {
					t.Errorf("duplicate error: %v", msg)
				} else {
					msgs.Insert(msg)
				}
			}
		})
	}
}

func TestReleaseNames(t *testing.T) {
	var testCases = []string{
		LatestReleaseName,
		InitialReleaseName,
		"foo",
	}
	for _, name := range testCases {
		stream := ReleaseStreamFor(name)
		if !IsReleaseStream(stream) {
			t.Errorf("stream %s for name %s was not identified as a release stream", stream, name)
		}
		if actual, expected := ReleaseNameFrom(stream), name; actual != expected {
			t.Errorf("parsed name %s from stream %s, but it was created for name %s", actual, stream, expected)
		}
	}

}

func TestLinkForImage(t *testing.T) {
	var testCases = []struct {
		stream, tag string
		expected    StepLink
	}{
		{
			stream:   "pipeline",
			tag:      "src",
			expected: InternalImageLink(PipelineImageStreamTagReferenceSource),
		},
		{
			stream:   "pipeline",
			tag:      "rpms",
			expected: InternalImageLink(PipelineImageStreamTagReferenceRPMs),
		},
		{
			stream:   "stable",
			tag:      "installer",
			expected: ReleaseImagesLink(LatestReleaseName),
		},
		{
			stream:   "stable-initial",
			tag:      "cli",
			expected: ReleaseImagesLink(InitialReleaseName),
		},
		{
			stream:   "stable-whatever",
			tag:      "hyperconverged-cluster-operator",
			expected: ReleaseImagesLink("whatever"),
		},
		{
			stream:   "release",
			tag:      "latest",
			expected: ReleasePayloadImageLink(LatestReleaseName),
		},
		{
			stream: "crazy",
			tag:    "tag",
		},
	}
	for _, testCase := range testCases {
		if diff := cmp.Diff(LinkForImage(testCase.stream, testCase.tag), testCase.expected, Comparer()); diff != "" {
			t.Errorf("got incorrect link for %s:%s: %v", testCase.stream, testCase.tag, diff)
		}
	}
}

func TestCIOperatorStepGraphMergeFromKeepsAllData(t *testing.T) {
	testCases := []struct {
		name         string
		existinGraph CIOperatorStepGraph
	}{
		{

			name: "Empty graph gets appended",
		},

		{
			name:         "Graph with one empty element, element gets merged into",
			existinGraph: CIOperatorStepGraph{{}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					existinGraphCopy := append(CIOperatorStepGraph{}, tc.existinGraph...)
					step := CIOperatorStepDetails{}
					fuzz.New().Funcs(func(r *ctrlruntimeclient.Object, _ fuzz.Continue) { *r = &corev1.Pod{} }).Fuzz(&step)
					if len(existinGraphCopy) == 1 {
						existinGraphCopy[0].StepName = step.StepName
					}
					existinGraphCopy.MergeFrom(step)
					if n := len(existinGraphCopy); n != 1 {
						t.Fatalf("expected graph to have exactly one element, had %d", n)
					}
					if diff := cmp.Diff(step, existinGraphCopy[0]); diff != "" {
						t.Errorf("original element differs from merged element: %s", diff)
					}
				})
			}
		})
	}
}

func TestCIOperatorStepWithDependenciesSerializationRoundTrips(t *testing.T) {
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			step := &CIOperatorStepDetailInfo{}
			fuzz.New().Funcs(func(r *ctrlruntimeclient.Object, _ fuzz.Continue) {
				p := &corev1.Pod{}
				p.APIVersion = "v1"
				p.Kind = "Pod"
				*r = p
			}).Fuzz(&step)

			serializedFirstPasss, err := json.Marshal(step)
			if err != nil {
				t.Fatalf("first serializiation failed: %v", err)
			}
			var stepRoundTripped CIOperatorStepDetailInfo
			if err := json.Unmarshal(serializedFirstPasss, &stepRoundTripped); err != nil {
				t.Fatalf("failed to unserialize: %v", err)
			}
			serializationSecondPass, err := json.Marshal(stepRoundTripped)
			if err != nil {
				t.Fatalf("second serialization failed: %v", err)
			}

			// Have to serialize them yet again, because the field order is not deterministic
			var o1 interface{}
			var o2 interface{}
			if err = json.Unmarshal(serializationSecondPass, &o1); err != nil {
				t.Fatalf("failed to re-marshal first serialization: %v", err)
			}
			if err = json.Unmarshal(serializationSecondPass, &o2); err != nil {
				t.Fatalf("failed to re-marshal first serialization: %v", err)
			}
			if diff := cmp.Diff(o1, o2); diff != "" {
				t.Errorf("Serializations differ: %s", diff)
			}
		})
	}
}
