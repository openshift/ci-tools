package api

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	fuzz "github.com/google/gofuzz"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/testhelper"
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
	requires  []StepLink
	creates   []StepLink
	name      string
	multiArch bool
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

func (f *fakeStep) IsMultiArch() bool           { return f.multiArch }
func (f *fakeStep) SetMultiArch(multiArch bool) { f.multiArch = multiArch }

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

type fakeSortLink struct {
	name string
}

func (l fakeSortLink) SatisfiedBy(lhs StepLink) bool {
	return l.name == lhs.(fakeSortLink).name
}

func (l fakeSortLink) UnsatisfiableError() string { return "" }

type fakeSortStep struct {
	name     string
	err      error
	requires []string
}

func (*fakeSortStep) Inputs() (InputDefinition, error)    { return nil, nil }
func (*fakeSortStep) Run(ctx context.Context) error       { return nil }
func (f *fakeSortStep) Name() string                      { return f.name }
func (*fakeSortStep) Description() string                 { return "" }
func (*fakeSortStep) Provides() ParameterMap              { return nil }
func (f *fakeSortStep) Validate() error                   { return f.err }
func (*fakeSortStep) Objects() []ctrlruntimeclient.Object { return nil }
func (f *fakeSortStep) IsMultiArch() bool                 { return false }
func (f *fakeSortStep) SetMultiArch(multiArch bool)       {}

func (f *fakeSortStep) Creates() []StepLink {
	return []StepLink{fakeSortLink{name: f.name}}
}

func (f *fakeSortStep) Requires() (ret []StepLink) {
	for _, r := range f.requires {
		ret = append(ret, fakeSortLink{name: r})
	}
	return
}

func TestTopologicalSort(t *testing.T) {
	t.Parallel()
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	root := fakeSortStep{name: "root"}
	src := fakeSortStep{name: "src", requires: []string{"root"}}
	bin := fakeSortStep{name: "bin", requires: []string{"src"}}
	img0 := fakeSortStep{name: "img0", requires: []string{"root", "bin"}}
	img1 := fakeSortStep{name: "img1", requires: []string{"bin"}}
	img2 := fakeSortStep{name: "img2", requires: []string{"root"}}
	missing0 := fakeSortStep{name: "missing0", requires: []string{"missing1"}}
	cycle0 := fakeSortStep{name: "cycle0"}
	cycle1 := fakeSortStep{name: "cycle1", requires: []string{"cycle0", "cycle3"}}
	cycle2 := fakeSortStep{name: "cycle2", requires: []string{"cycle0", "cycle1"}}
	cycle3 := fakeSortStep{name: "cycle3", requires: []string{"cycle0", "cycle2"}}
	for _, tc := range []struct {
		name     string
		steps    []Step
		expected []error
	}{{
		name: "empty graph",
	}, {
		name:  "valid graph",
		steps: []Step{&root, &src, &bin, &img0, &img1, &img2},
	}, {
		name:  "repeated path",
		steps: []Step{&root, &src, &bin, &img0, &img1, &img2},
	}, {
		name: "missing dependency",
		expected: []error{
			errors.New(`step missing0 is missing dependencies: <api.fakeSortLink{name:"missing1"}>`),
			errors.New("steps are missing dependencies"),
		},
		steps: []Step{&missing0},
	}, {
		name: "cycle",
		expected: []error{
			errors.New("cycle in graph: cycle0 -> cycle1 -> cycle2 -> cycle3 -> cycle1"),
			errors.New("cycle in graph: cycle0 -> cycle2 -> cycle3 -> cycle1 -> cycle2"),
			errors.New("cycle in graph: cycle0 -> cycle3 -> cycle1 -> cycle2 -> cycle3"),
		},
		steps: []Step{&cycle0, &cycle1, &cycle2, &cycle3},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			steps := make([]Step, len(tc.steps))
			copy(steps, tc.steps)
			rnd.Shuffle(len(steps), func(i, j int) {
				steps[i], steps[j] = steps[j], steps[i]
			})
			nodes, err := BuildGraph(steps).TopologicalSort()
			var stepNames, nodeNames []string
			if tc.expected == nil {
				for _, s := range tc.steps {
					stepNames = append(stepNames, s.(*fakeSortStep).name)
				}
				for _, n := range nodes {
					nodeNames = append(nodeNames, n.Step.Name())
				}
				sort.Slice(stepNames, func(i, j int) bool {
					return stepNames[i] < stepNames[j]
				})
				sort.Slice(nodeNames, func(i, j int) bool {
					return nodeNames[i] < nodeNames[j]
				})
			}
			sort.Slice(err, func(i, j int) bool {
				return err[i].Error() < err[j].Error()
			})
			testhelper.Diff(t, "nodes", stepNames, nodeNames)
			testhelper.Diff(t, "errors", err, tc.expected, testhelper.EquateErrorMessage)
			for i, n0 := range nodes {
				s := n0.Step.(*fakeSortStep)
			next1:
				for _, r := range s.requires {
					for _, n1 := range nodes[:i] {
						if n1.Step.Name() == r {
							continue next1
						}
					}
					t.Errorf("dependency %s not before %s", r, s.name)
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

func TestResolveMultiArch(t *testing.T) {
	testCases := []struct {
		name     string
		nodes    []*StepNode
		expected []*StepNode
	}{
		{
			name: "no multi-arch, no change",
			nodes: []*StepNode{
				{
					Step: &fakeStep{name: "step1"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
						{Step: &fakeStep{name: "step4", multiArch: false}},
					},
				},
			},
			expected: []*StepNode{
				{
					Step: &fakeStep{name: "step1", multiArch: false},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
						{Step: &fakeStep{name: "step4", multiArch: false}}},
				},
			},
		},
		{
			name: "one or more children are multi-arch",
			nodes: []*StepNode{
				{
					Step: &fakeStep{name: "step1"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step2", multiArch: true}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
						{Step: &fakeStep{name: "step4", multiArch: true}},
					},
				},
			},
			expected: []*StepNode{
				{
					Step: &fakeStep{name: "step1", multiArch: true},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step2", multiArch: true}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
						{Step: &fakeStep{name: "step4", multiArch: true}}},
					MultiArchReasons: []string{"step2", "step4"},
				},
			},
		},
		{
			name: "multiple nodes - one or more children are multi-arch",
			nodes: []*StepNode{
				{
					Step: &fakeStep{name: "step10"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: false}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
					},
				},
				{
					Step: &fakeStep{name: "step20"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: true}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: true}},
					},
				},
				{
					Step: &fakeStep{name: "step30"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: true}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
					},
				},
			},
			expected: []*StepNode{
				{
					Step: &fakeStep{name: "step10"},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: false}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
					},
				},
				{
					Step: &fakeStep{name: "step20", multiArch: true},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: true}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: true}},
					},
					MultiArchReasons: []string{"step1", "step3"},
				},
				{
					Step: &fakeStep{name: "step30", multiArch: true},
					Children: []*StepNode{
						{Step: &fakeStep{name: "step1", multiArch: true}},
						{Step: &fakeStep{name: "step2", multiArch: false}},
						{Step: &fakeStep{name: "step3", multiArch: false}},
					},
					MultiArchReasons: []string{"step1"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ResolveMultiArch(tc.nodes)
			if diff := cmp.Diff(tc.expected, tc.nodes, cmp.AllowUnexported(fakeStep{})); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
