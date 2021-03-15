package release

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/diff"

	imageapi "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestToPromote(t *testing.T) {
	var testCases = []struct {
		name             string
		config           api.PromotionConfiguration
		images           []api.ProjectDirectoryImageBuildStepConfiguration
		requiredImages   sets.String
		expectedBySource map[string]string
		expectedNames    sets.String
	}{
		{
			name: "disabled config returns nothing",
			config: api.PromotionConfiguration{
				Disabled: true,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{},
			expectedNames:    sets.NewString(),
		},
		{
			name: "enabled config returns input list",
			config: api.PromotionConfiguration{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz"},
			expectedNames:    sets.NewString("foo", "bar", "baz"),
		},
		{
			name: "enabled config with exclude returns filtered input list",
			config: api.PromotionConfiguration{
				ExcludedImages: []string{"foo"},
				Disabled:       false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"bar": "bar", "baz": "baz"},
			expectedNames:    sets.NewString("bar", "baz"),
		},
		{
			name: "enabled config with optional image returns subset of input list",
			config: api.PromotionConfiguration{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"foo": "foo", "baz": "baz"},
			expectedNames:    sets.NewString("foo", "baz"),
		},
		{
			name: "enabled config with optional but required image returns full input list",
			config: api.PromotionConfiguration{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString("bar"),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz"},
			expectedNames:    sets.NewString("foo", "bar", "baz"),
		},
		{
			name: "enabled config with additional images returns appended input list",
			config: api.PromotionConfiguration{
				AdditionalImages: map[string]string{"boo": "ah"},
				Disabled:         false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz", "boo": "ah"},
			expectedNames:    sets.NewString("foo", "bar", "baz", "boo"),
		},
		{
			name: "enabled config with excludes and additional images returns filtered, appended input list",
			config: api.PromotionConfiguration{
				ExcludedImages:   []string{"foo"},
				AdditionalImages: map[string]string{"boo": "ah"},
				Disabled:         false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"bar": "bar", "baz": "baz", "boo": "ah"},
			expectedNames:    sets.NewString("bar", "baz", "boo"),
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			bySource, names := toPromote(test.config, test.images, test.requiredImages)
			if actual, expected := bySource, test.expectedBySource; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect tags by source: %s", test.name, diff.ObjectDiff(actual, expected))
			}

			if actual, expected := names, test.expectedNames; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect names: %s", test.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestPromotedTags(t *testing.T) {
	var testCases = []struct {
		name     string
		input    *api.ReleaseBuildConfiguration
		expected []api.ImageStreamTagReference
	}{
		{
			name:     "no promotion, no output",
			input:    &api.ReleaseBuildConfiguration{},
			expected: nil,
		},
		{
			name: "promoted image means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
				},
			},
			expected: []api.ImageStreamTagReference{{
				Namespace: "roger",
				Name:      "fred",
				Tag:       "foo",
			}},
		},
		{
			name: "promoted image but disabled promotion means no output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
					Disabled:  true,
				},
			},
			expected: nil,
		},
		{
			name: "promoted image by tag means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
				},
			},
			expected: []api.ImageStreamTagReference{{
				Namespace: "roger",
				Name:      "foo",
				Tag:       "fred",
			}},
		},
		{
			name: "promoted additional image with rename",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
					AdditionalImages: map[string]string{
						"output": "src",
					},
				},
			},
			expected: []api.ImageStreamTagReference{{
				Namespace: "roger",
				Name:      "foo",
				Tag:       "fred",
			}, {
				Namespace: "roger",
				Name:      "output",
				Tag:       "fred",
			}},
		},
		{
			name: "disabled image",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace:      "roger",
					Tag:            "fred",
					ExcludedImages: []string{"foo"},
				},
			},
			expected: nil,
		},
		{
			name: "promotion set and binaries built, means binaries promoted",
			input: &api.ReleaseBuildConfiguration{
				Images:              []api.ProjectDirectoryImageBuildStepConfiguration{},
				BinaryBuildCommands: "something",
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			expected: []api.ImageStreamTagReference{{
				Namespace: "build-cache",
				Name:      "org-repo",
				Tag:       "branch",
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotedTags(testCase.input), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect promoted tags: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestPromotedTagsWithRequiredImages(t *testing.T) {
	var testCases = []struct {
		name     string
		input    *api.ReleaseBuildConfiguration
		images   sets.String
		expected map[string]api.ImageStreamTagReference
		names    sets.String
	}{
		{
			name:  "no promotion, no output",
			input: &api.ReleaseBuildConfiguration{},
		},
		{
			name: "promoted image means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
				},
			},
			expected: map[string]api.ImageStreamTagReference{"foo": {
				Namespace: "roger",
				Name:      "fred",
				Tag:       "foo",
			}},
		},
		{
			name: "optional image is ignored means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
					{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
				},
			},
			expected: map[string]api.ImageStreamTagReference{"foo": {
				Namespace: "roger",
				Name:      "fred",
				Tag:       "foo",
			}},
		},
		{
			name: "optional image that's required means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo"), Optional: true},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
				},
			},
			images: sets.NewString("foo"),
			expected: map[string]api.ImageStreamTagReference{"foo": {
				Namespace: "roger",
				Name:      "fred",
				Tag:       "foo",
			}},
		},
		{
			name: "promoted image but disabled promotion means no output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Name:      "fred",
					Disabled:  true,
				},
			},
			expected: nil,
		},
		{
			name: "promoted image by tag means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
				},
			},
			expected: map[string]api.ImageStreamTagReference{"foo": {
				Namespace: "roger",
				Name:      "foo",
				Tag:       "fred",
			}},
		},
		{
			name: "promoted additional image with rename",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
					AdditionalImages: map[string]string{
						"output": "src",
					},
				},
			},
			expected: map[string]api.ImageStreamTagReference{"foo": {
				Namespace: "roger",
				Name:      "foo",
				Tag:       "fred",
			}, "src": {
				Namespace: "roger",
				Name:      "output",
				Tag:       "fred",
			}},
		},
		{
			name: "disabled image",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace:      "roger",
					Tag:            "fred",
					ExcludedImages: []string{"foo"},
				},
			},
			expected: map[string]api.ImageStreamTagReference{},
		},
		{
			name: "promotion set and binaries built, means binaries promoted",
			input: &api.ReleaseBuildConfiguration{
				Images:              []api.ProjectDirectoryImageBuildStepConfiguration{},
				BinaryBuildCommands: "something",
				PromotionConfiguration: &api.PromotionConfiguration{
					Namespace: "roger",
					Tag:       "fred",
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			expected: map[string]api.ImageStreamTagReference{"bin": {
				Namespace: "build-cache",
				Name:      "org-repo",
				Tag:       "branch",
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mapping, _ := PromotedTagsWithRequiredImages(testCase.input, testCase.images)
			if actual, expected := mapping, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect promoted tags: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestBuildCacheFor(t *testing.T) {
	var testCases = []struct {
		input  api.Metadata
		output api.ImageStreamTagReference
	}{
		{
			input: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			output: api.ImageStreamTagReference{
				Namespace: "build-cache",
				Name:      "org-repo",
				Tag:       "branch",
			},
		},
		{
			input: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			output: api.ImageStreamTagReference{
				Namespace: "build-cache",
				Name:      "org-repo",
				Tag:       "branch-variant",
			},
		},
	}
	for _, testCase := range testCases {
		if diff := cmp.Diff(testCase.output, BuildCacheFor(testCase.input)); diff != "" {
			t.Errorf("got incorrect ist for build cache: %v", diff)
		}
	}
}

func TestGetPromotionPod(t *testing.T) {
	var testCases = []struct {
		name        string
		imageMirror map[string]string
		namespace   string
		expected    *coreapi.Pod
	}{
		{
			name: "basic case",
			imageMirror: map[string]string{
				"docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:afd71aa3cbbf7d2e00cd8696747b2abf164700147723c657919c20b13d13ec62": "registy.ci.openshift.org/ci/applyconfig:latest",
				"docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb":                                                              "registy.ci.openshift.org/ci/bin:latest",
			},
			namespace: "ci-op-zyvwvffx",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testhelper.CompareWithFixture(t, getPromotionPod(testCase.imageMirror, testCase.namespace))
		})
	}
}

func TestGetImageMirror(t *testing.T) {
	var testCases = []struct {
		name     string
		tags     map[string]api.ImageStreamTagReference
		pipeline *imageapi.ImageStream
		expected map[string]string
	}{
		{
			name: "empty input",
		},
		{
			name:     "nil tags",
			pipeline: &imageapi.ImageStream{},
		},
		{
			name: "basic case",
			tags: map[string]api.ImageStreamTagReference{
				"b": {
					Namespace: "ci",
					Name:      "a",
					Tag:       "latest",
				},
				"d": {
					Namespace: "ci",
					Name:      "c",
					Tag:       "latest",
				},
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{
						{
							Tag: "b",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
								},
							},
						},
						{
							Tag: "d",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:ddd",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb": "registry.ci.openshift.org/ci/a:latest",
				"docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:ddd": "registry.ci.openshift.org/ci/c:latest",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := getImageMirrorTarget(testCase.tags, testCase.pipeline), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect ImageMirror mapping: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}

func TestGetPublicImageReference(t *testing.T) {
	var testCases = []struct {
		name                        string
		dockerImageReference        string
		publicDockerImageRepository string
		expected                    string
	}{
		{
			name:                        "basic case",
			dockerImageReference:        "docker-registry.default.svc:5000/ci-op-bgqwwknr/pipeline@sha256:d8385fb539f471d4f41da131366b559bb90eeeeca2edd265e10d7c2aa052a1af",
			publicDockerImageRepository: "registry.svc.ci.openshift.org/ci-op-bgqwwknr/pipeline",
			expected:                    "registry.svc.ci.openshift.org/ci-op-bgqwwknr/pipeline@sha256:d8385fb539f471d4f41da131366b559bb90eeeeca2edd265e10d7c2aa052a1af",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := getPublicImageReference(testCase.dockerImageReference, testCase.publicDockerImageRepository), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect public image reference: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}
