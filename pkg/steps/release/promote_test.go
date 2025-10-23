package release

import (
	"fmt"
	"reflect"
	"sort"
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
		config           api.PromotionTarget
		images           []api.ProjectDirectoryImageBuildStepConfiguration
		requiredImages   sets.Set[string]
		expectedBySource map[string]string
		expectedNames    sets.Set[string]
	}{
		{
			name: "disabled config returns nothing",
			config: api.PromotionTarget{
				Disabled: true,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{},
			expectedNames:    sets.New[string](),
		},
		{
			name: "enabled config returns input list",
			config: api.PromotionTarget{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz"},
			expectedNames:    sets.New[string]("foo", "bar", "baz"),
		},
		{
			name: "enabled config with exclude returns filtered input list",
			config: api.PromotionTarget{
				ExcludedImages: []string{"foo"},
				Disabled:       false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{"bar": "bar", "baz": "baz"},
			expectedNames:    sets.New[string]("bar", "baz"),
		},
		{
			name: "enabled config with optional image returns subset of input list",
			config: api.PromotionTarget{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{"foo": "foo", "baz": "baz"},
			expectedNames:    sets.New[string]("foo", "baz"),
		},
		{
			name: "enabled config with optional but required image returns full input list",
			config: api.PromotionTarget{
				Disabled: false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string]("bar"),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz"},
			expectedNames:    sets.New[string]("foo", "bar", "baz"),
		},
		{
			name: "enabled config with additional images returns appended input list",
			config: api.PromotionTarget{
				AdditionalImages: map[string]string{"boo": "ah"},
				Disabled:         false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{"foo": "foo", "bar": "bar", "baz": "baz", "boo": "ah"},
			expectedNames:    sets.New[string]("foo", "bar", "baz", "boo"),
		},
		{
			name: "enabled config with excludes and additional images returns filtered, appended input list",
			config: api.PromotionTarget{
				ExcludedImages:   []string{"foo"},
				AdditionalImages: map[string]string{"boo": "ah"},
				Disabled:         false,
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.New[string](),
			expectedBySource: map[string]string{"bar": "bar", "baz": "baz", "boo": "ah"},
			expectedNames:    sets.New[string]("bar", "baz", "boo"),
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
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
					}},
				},
			},
			expected: []api.ImageStreamTagReference{
				{Namespace: "roger", Name: "fred", Tag: "foo"},
			},
		},
		{
			name: "promoted image but disabled promotion means no output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
						Disabled:  true,
					}},
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
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
					}},
				},
			},
			expected: []api.ImageStreamTagReference{
				{Namespace: "roger", Name: "foo", Tag: "fred"},
			},
		},
		{
			name: "promoted additional image with rename",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
						AdditionalImages: map[string]string{
							"output": "src",
						},
					}},
				},
			},
			expected: []api.ImageStreamTagReference{
				{Namespace: "roger", Name: "foo", Tag: "fred"},
				{Namespace: "roger", Name: "output", Tag: "fred"},
			},
		},
		{
			name: "disabled image",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace:      "roger",
						Tag:            "fred",
						ExcludedImages: []string{"foo"},
					}},
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
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
					}},
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			expected: []api.ImageStreamTagReference{
				{Namespace: "build-cache", Name: "org-repo", Tag: "branch"},
			},
		},
		{
			name: "promotion with AdditionalImages: many to one",
			input: &api.ReleaseBuildConfiguration{
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ocp",
						Name:      "4.6",
						AdditionalImages: map[string]string{
							"base":   "base-8",
							"base-7": "base-7",
							"base-8": "base-8",
						},
					}},
				},
				Metadata: api.Metadata{
					Org:    "openshift",
					Repo:   "images",
					Branch: "release-4.6",
				},
			},
			expected: []api.ImageStreamTagReference{
				{Namespace: "ocp", Name: "4.6", Tag: "base"},
				{Namespace: "ocp", Name: "4.6", Tag: "base-7"},
				{Namespace: "ocp", Name: "4.6", Tag: "base-8"},
			},
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
		name                   string
		input                  *api.ReleaseBuildConfiguration
		options                []PromotedTagsOption
		expected               map[string][]api.ImageStreamTagReference
		expectedRequiredImages sets.Set[string]
	}{
		{
			name:                   "no promotion, no output",
			input:                  &api.ReleaseBuildConfiguration{},
			expected:               map[string][]api.ImageStreamTagReference{},
			expectedRequiredImages: sets.New[string](),
		},
		{
			name: "promoted image means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
					}},
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "fred", Tag: "foo"},
				},
			},
			expectedRequiredImages: sets.New[string]("foo"),
		},
		{
			name: "optional image is ignored means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
					{To: api.PipelineImageStreamTagReference("bar"), Optional: true},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
					}},
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "fred", Tag: "foo"},
				},
			},
			expectedRequiredImages: sets.New[string]("foo"),
		},
		{
			name: "optional image that's required means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo"), Optional: true},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
					}},
				},
			},
			options: []PromotedTagsOption{WithRequiredImages(sets.New[string]("foo"))},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "fred", Tag: "foo"},
				},
			},
			expectedRequiredImages: sets.New[string]("foo"),
		},
		{
			name: "promoted image but disabled promotion means no output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Name:      "fred",
						Disabled:  true,
					}},
				},
			},
			expected:               map[string][]api.ImageStreamTagReference{},
			expectedRequiredImages: sets.New[string](),
		},
		{
			name: "promoted image by tag means output tags",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
					}},
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "foo", Tag: "fred"},
				},
			},
			expectedRequiredImages: sets.New[string]("foo"),
		},
		{
			name: "promoted image tagged by commit means an additional tag",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace:   "roger",
						Tag:         "fred",
						TagByCommit: true,
					}},
				},
			},
			options: []PromotedTagsOption{WithCommitSha("sha")},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "foo", Tag: "fred"},
					{Namespace: "roger", Name: "foo", Tag: "sha"},
				},
			},
			expectedRequiredImages: sets.New[string]("foo"),
		},
		{
			name: "promoted additional image with rename",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
						AdditionalImages: map[string]string{
							"output": "src",
						},
					}},
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"foo": {
					{Namespace: "roger", Name: "foo", Tag: "fred"},
				},
				"src": {
					{Namespace: "roger", Name: "output", Tag: "fred"},
				},
			},
			expectedRequiredImages: sets.New[string]("output", "foo"),
		},
		{
			name: "disabled image",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: api.PipelineImageStreamTagReference("foo")},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace:      "roger",
						Tag:            "fred",
						ExcludedImages: []string{"foo"},
					}},
				},
			},
			expected:               map[string][]api.ImageStreamTagReference{},
			expectedRequiredImages: sets.New[string](),
		},
		{
			name: "promotion set and binaries built, means binaries promoted",
			input: &api.ReleaseBuildConfiguration{
				Images:              []api.ProjectDirectoryImageBuildStepConfiguration{},
				BinaryBuildCommands: "something",
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
					}},
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"bin": {{Namespace: "build-cache", Name: "org-repo", Tag: "branch"}},
			},
			expectedRequiredImages: sets.New[string](),
		},
		{
			name: "promotion set and binaries built, build cache disabled means no binaries promoted",
			input: &api.ReleaseBuildConfiguration{
				Images:              []api.ProjectDirectoryImageBuildStepConfiguration{},
				BinaryBuildCommands: "something",
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "roger",
						Tag:       "fred",
					}},
					DisableBuildCache: true,
				},
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
			},
			expected:               map[string][]api.ImageStreamTagReference{},
			expectedRequiredImages: sets.New[string](),
		},
		{
			name: "promotion with AdditionalImages: many to one",
			input: &api.ReleaseBuildConfiguration{
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ocp",
						Name:      "4.6",
						AdditionalImages: map[string]string{
							"base":   "base-8",
							"base-7": "base-7",
							"base-8": "base-8",
						},
					}},
				},
				Metadata: api.Metadata{
					Org:    "openshift",
					Repo:   "images",
					Branch: "release-4.6",
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"base-7": {
					{Namespace: "ocp", Name: "4.6", Tag: "base-7"},
				},
				"base-8": {
					{Namespace: "ocp", Name: "4.6", Tag: "base"},
					{Namespace: "ocp", Name: "4.6", Tag: "base-8"},
				},
			},
			expectedRequiredImages: sets.New[string]("base", "base-7", "base-8"),
		},
		{
			name: "promotion with multiple to stanzas",
			input: &api.ReleaseBuildConfiguration{
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						Namespace: "ocp",
						Name:      "4.6",
						AdditionalImages: map[string]string{
							"base":   "base-8",
							"base-7": "base-7",
							"base-8": "base-8",
						},
					}, {
						ExcludedImages: []string{"*"},
						AdditionalImages: map[string]string{
							"other": "base",
						},
						Namespace: "extra",
						Tag:       "latest",
					}},
				},
				Metadata: api.Metadata{
					Org:    "openshift",
					Repo:   "images",
					Branch: "release-4.6",
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"base": {
					{Namespace: "extra", Name: "other", Tag: "latest"},
				},
				"base-7": {
					{Namespace: "ocp", Name: "4.6", Tag: "base-7"},
				},
				"base-8": {
					{Namespace: "ocp", Name: "4.6", Tag: "base"},
					{Namespace: "ocp", Name: "4.6", Tag: "base-8"},
				},
			},
			expectedRequiredImages: sets.New[string]("base", "base-7", "base-8", "other"),
		},
		{
			name: "promotion only cli-ocm to ci",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						From: "cli",
						To:   "cli-ocm",
					},
					{
						From: "src",
						To:   "cli",
					},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{
						{
							ExcludedImages: []string{api.PromotionExcludeImageWildcard},
							Namespace:      "ci",
							Name:           "cli-ocm",
							AdditionalImages: map[string]string{
								"latest": "cli-ocm",
							},
						},
						{
							Namespace: "ocp",
							Name:      "4.20",
						},
					},
				},
				Metadata: api.Metadata{
					Org:    "openshift",
					Repo:   "oc",
					Branch: "master",
				},
			},
			expected: map[string][]api.ImageStreamTagReference{
				"cli-ocm": {
					{Namespace: "ci", Name: "cli-ocm", Tag: "latest"},
					{Namespace: "ocp", Name: "4.20", Tag: "cli-ocm"},
				},
				"cli": {
					{Namespace: "ocp", Name: "4.20", Tag: "cli"},
				},
			},
			expectedRequiredImages: sets.New[string]("latest", "cli-ocm", "cli"),
		},
		{
			name: "exclude everything",
			input: &api.ReleaseBuildConfiguration{
				Images: []api.ProjectDirectoryImageBuildStepConfiguration{
					{To: "img_a"},
					{To: "img_b"},
				},
				PromotionConfiguration: &api.PromotionConfiguration{
					Targets: []api.PromotionTarget{{
						ExcludedImages: []string{api.PromotionExcludeImageWildcard},
					}},
				},
			},
			expected:               map[string][]api.ImageStreamTagReference{},
			expectedRequiredImages: sets.New[string](),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mapping, requiredImages := PromotedTagsWithRequiredImages(testCase.input, testCase.options...)
			actual, expected := mapping, testCase.expected
			if diff := cmp.Diff(actual, expected); diff != "" {
				t.Errorf("%s: got incorrect promoted tags: %v", testCase.name, diff)
			}
			if !requiredImages.Equal(testCase.expectedRequiredImages) {
				actual, expected := requiredImages.UnsortedList(), testCase.expectedRequiredImages.UnsortedList()
				sort.Strings(actual)
				sort.Strings(expected)
				t.Errorf("%s: got incorrect requiredImages: %s", testCase.name, cmp.Diff(actual, expected))
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
		if diff := cmp.Diff(testCase.output, api.BuildCacheFor(testCase.input)); diff != "" {
			t.Errorf("got incorrect ist for build cache: %v", diff)
		}
	}
}

func TestGetPromotionPod(t *testing.T) {
	var testCases = []struct {
		name              string
		stepName          string
		imageMirror       map[string]string
		nodeArchitectures []string
		namespace         string
		expected          *coreapi.Pod
		expectedErr       error
	}{
		{
			name:              "basic case",
			stepName:          "promotion",
			nodeArchitectures: []string{"amd64"},
			imageMirror: map[string]string{
				"registry.ci.openshift.org/ci/applyconfig:latest": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:afd71aa3cbbf7d2e00cd8696747b2abf164700147723c657919c20b13d13ec62",
				"registry.ci.openshift.org/ci/bin:latest":         "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
			},
			namespace: "ci-op-zyvwvffx",
		},
		{
			name:              "promotion-quay",
			stepName:          "promotion-quay",
			nodeArchitectures: []string{"amd64"},
			imageMirror: map[string]string{
				"quay.io/openshift/ci:20240603235401_prune_ci_a_latest": "quay.io/openshift/ci:ci_a_latest",
				"quay.io/openshift/ci:20240603235401_prune_ci_c_latest": "quay.io/openshift/ci:ci_c_latest",
				"quay.io/openshift/ci:ci_a_latest":                      "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"quay.io/openshift/ci:ci_c_latest":                      "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:ddd",
				"registry.ci.openshift.org/ci/ci-quay:${component}":     "quay-proxy.ci.openshift.org/openshift/ci:ci_a_latest",
				"registry.ci.openshift.org/ci/${component}-quay:c":      "quay-proxy.ci.openshift.org/openshift/ci:ci_c_latest",
			},
			namespace: "ci-op-9bdij1f6",
		},
		{
			name:              "basic case - arm64 only",
			stepName:          "promotion",
			nodeArchitectures: []string{"arm64"},
			imageMirror: map[string]string{
				"registry.ci.openshift.org/ci/applyconfig:latest": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:afd71aa3cbbf7d2e00cd8696747b2abf164700147723c657919c20b13d13ec62",
				"registry.ci.openshift.org/ci/bin:latest":         "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
			},
			namespace: "ci-op-zyvwvffx",
		},
		{
			name:              "basic case - multi architecture",
			stepName:          "promotion",
			nodeArchitectures: []string{"amd64", "arm64"},
			imageMirror: map[string]string{
				"registry.ci.openshift.org/ci/applyconfig:latest": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:afd71aa3cbbf7d2e00cd8696747b2abf164700147723c657919c20b13d13ec62",
				"registry.ci.openshift.org/ci/bin:latest":         "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
			},
			namespace: "ci-op-zyvwvffx",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testhelper.CompareWithFixture(t, getPromotionPod(testCase.imageMirror, "20240603235401", testCase.namespace, testCase.stepName, "4.14", testCase.nodeArchitectures))
		})
	}
}

func TestGetImageMirror(t *testing.T) {
	var testCases = []struct {
		name           string
		stepName       string
		tags           map[string][]api.ImageStreamTagReference
		pipeline       *imageapi.ImageStream
		registry       string
		mirrorFunc     func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string)
		targetNameFunc func(string, api.PromotionTarget) string
		expected       map[string]string
	}{
		{
			name: "empty input",
		},
		{
			name:     "nil tags",
			pipeline: &imageapi.ImageStream{},
		},
		{
			name:     "basic case",
			stepName: "promotion",
			tags: map[string][]api.ImageStreamTagReference{
				"b": {
					{Namespace: "ci", Name: "a", Tag: "latest"},
				},
				"d": {
					{Namespace: "ci", Name: "c", Tag: "latest"},
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
			registry:   "registry.ci.openshift.org",
			mirrorFunc: api.DefaultMirrorFunc,
			expected: map[string]string{
				"registry.ci.openshift.org/ci/a:latest": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"registry.ci.openshift.org/ci/c:latest": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:ddd",
			},
		},
		{
			name: "image promoted to multiple names",
			tags: map[string][]api.ImageStreamTagReference{
				"b": {
					{Namespace: "ci", Name: "a", Tag: "promoted"},
					{Namespace: "ci", Name: "a", Tag: "also-promoted"},
				},
				"d": {
					{Namespace: "ci", Name: "c", Tag: "latest"},
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
			registry:   "registry.ci.openshift.org",
			mirrorFunc: api.DefaultMirrorFunc,
			expected: map[string]string{
				"registry.ci.openshift.org/ci/a:promoted":      "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"registry.ci.openshift.org/ci/a:also-promoted": "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"registry.ci.openshift.org/ci/c:latest":        "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:ddd",
			},
		},
		{
			name: "quay.io",
			tags: map[string][]api.ImageStreamTagReference{
				"b": {
					{Namespace: "ci", Name: "a", Tag: "latest"},
				},
				"d": {
					{Namespace: "ci", Name: "c", Tag: "latest"},
				},
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline",
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
			registry:   "quay.io/openshift/ci",
			mirrorFunc: api.QuayMirrorFunc,
			expected: map[string]string{
				"quay.io/openshift/ci:20240603235401_prune_ci_a_latest": "quay.io/openshift/ci:ci_a_latest",
				"quay.io/openshift/ci:20240603235401_prune_ci_c_latest": "quay.io/openshift/ci:ci_c_latest",
				"quay.io/openshift/ci:ci_a_latest":                      "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"quay.io/openshift/ci:ci_c_latest":                      "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:ddd",
			},
		},
		{
			name: "image promoted to multiple names: quay.io",
			tags: map[string][]api.ImageStreamTagReference{
				"b": {
					{Namespace: "ci", Name: "a", Tag: "promoted"},
					{Namespace: "ci", Name: "a", Tag: "also-promoted"},
				},
				"d": {
					{Namespace: "ci", Name: "c", Tag: "latest"},
				},
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline",
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
			registry:   "quay.io/openshift/ci",
			mirrorFunc: api.QuayMirrorFunc,
			expected: map[string]string{
				"quay.io/openshift/ci:20240603235401_prune_ci_a_promoted":      "quay.io/openshift/ci:ci_a_promoted",
				"quay.io/openshift/ci:20240603235401_prune_ci_a_also-promoted": "quay.io/openshift/ci:ci_a_also-promoted",
				"quay.io/openshift/ci:20240603235401_prune_ci_c_latest":        "quay.io/openshift/ci:ci_c_latest",
				"quay.io/openshift/ci:ci_a_also-promoted":                      "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"quay.io/openshift/ci:ci_a_promoted":                           "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:bbb",
				"quay.io/openshift/ci:ci_c_latest":                             "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:ddd",
			},
		},
		{
			name: "quay-proxy with targetNameFunc",
			tags: map[string][]api.ImageStreamTagReference{
				"vertical-pod-autoscaler": {
					{Namespace: "ocp", Name: "4.22", Tag: "vertical-pod-autoscaler"},
				},
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.build02.ci.openshift.org/ci-op-y2n8rsh3/pipeline",
					Tags: []imageapi.NamedTagEventList{
						{
							Tag: "vertical-pod-autoscaler",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:vpa",
								},
							},
						},
					},
				},
			},
			registry: "registry.ci.openshift.org",
			mirrorFunc: func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string) {
				proxyTarget := fmt.Sprintf("quay-proxy.ci.openshift.org/openshift/ci:%s_%s_%s", tag.Namespace, tag.Name, tag.Tag)
				imageMirror[target] = proxyTarget
			},
			targetNameFunc: func(registry string, config api.PromotionTarget) string {
				return fmt.Sprintf("%s/%s/%s-quay:${component}", registry, config.Namespace, config.Name)
			},
			expected: map[string]string{
				"registry.ci.openshift.org/ocp/4.22-quay:vertical-pod-autoscaler": "quay-proxy.ci.openshift.org/openshift/ci:ocp_4.22_vertical-pod-autoscaler",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, _ := getImageMirrorTarget(testCase.tags, testCase.pipeline, testCase.registry, "20240603235401", testCase.mirrorFunc, testCase.targetNameFunc); !reflect.DeepEqual(actual, testCase.expected) {
				t.Errorf("%s: got incorrect ImageMirror mapping: %v", testCase.name, diff.ObjectDiff(actual, testCase.expected))
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
