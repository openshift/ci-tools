package release

import (
	"reflect"
	"testing"

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
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotedTags(testCase.input), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect promoted tags: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
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
		config   api.PromotionConfiguration
		tags     map[string]string
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
			name: "basic case: empty config.Name",
			config: api.PromotionConfiguration{
				Namespace: "ci",
				Tag:       "latest",
			},
			tags: map[string]string{
				"a": "b",
				"c": "d",
				"x": "y",
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.svc.ci.openshift.org/ci-op-y2n8rsh3/pipeline",
					Tags: []imageapi.NamedTagEventList{
						{
							Tag: "b",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:bbb",
									Image:                "sha256:bbb",
								},
							},
						},
						{
							Tag: "d",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "docker-registry.default.svc:5000/ci-op-y2n8rsh3/pipeline@sha256:ddd",
									Image:                "sha256:ddd",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{"registry.svc.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:bbb": "registry.ci.openshift.org/ci/a:latest", "registry.svc.ci.openshift.org/ci-op-y2n8rsh3/pipeline@sha256:ddd": "registry.ci.openshift.org/ci/c:latest"},
		},
		{
			name: "basic case: config.Name",
			config: api.PromotionConfiguration{
				Namespace: "ci",
				Name:      "name",
				Tag:       "latest",
			},
			tags: map[string]string{
				"a": "b",
				"c": "d",
				"x": "y",
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.build02.ci.openshift.org/ci-op-q1ix6b8x/pipeline",
					Tags: []imageapi.NamedTagEventList{
						{
							Tag: "b",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci-op-q1ix6b8x/pipeline@sha256:bbb",
									Image:                "sha256:bbb",
								},
							},
						},
						{
							Tag: "d",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "image-registry.openshift-image-registry.svc:5000/ci-op-q1ix6b8x/pipeline@sha256:ddd",
									Image:                "sha256:ddd",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{"registry.build02.ci.openshift.org/ci-op-q1ix6b8x/pipeline@sha256:bbb": "registry.ci.openshift.org/ci/name:a", "registry.build02.ci.openshift.org/ci-op-q1ix6b8x/pipeline@sha256:ddd": "registry.ci.openshift.org/ci/name:c"},
		},
		{
			name: "promote machine-os-content",
			config: api.PromotionConfiguration{
				Namespace: "ocp",
				Name:      "4.7",
				Tag:       "does-not-matter",
			},
			tags: map[string]string{
				"machine-os-content": "machine-os-content",
			},
			pipeline: &imageapi.ImageStream{
				Status: imageapi.ImageStreamStatus{
					PublicDockerImageRepository: "registry.build01.ci.openshift.org/ci-op-9qkmyvrz/pipeline",
					Tags: []imageapi.NamedTagEventList{
						{
							Tag: "machine-os-content",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:a32077727aa2ef96a1e2371dbcc53ba06f3d9727e836b72be0f0dd4513937e1e",
									Image:                "sha256:a32077727aa2ef96a1e2371dbcc53ba06f3d9727e836b72be0f0dd4513937e1e",
								},
							},
						},
						{
							Tag: "ocp-4.5-upi-installer",
							Items: []imageapi.TagEvent{
								{
									DockerImageReference: "registry.ci.openshift.org/ocp/4.5@sha256:f5a9664ee7828336c02e0531cec0e238b794a72f36ce6350b8d7d56d5413a86f",
									Image:                "sha256:dbbacecc49b088458781c16f3775f2a2ec7521079034a7ba499c8b0bb7f86875",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{"registry.build01.ci.openshift.org/ci-op-9qkmyvrz/pipeline@sha256:a32077727aa2ef96a1e2371dbcc53ba06f3d9727e836b72be0f0dd4513937e1e": "registry.ci.openshift.org/ocp/4.7:machine-os-content"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := getImageMirrorTarget(testCase.config, testCase.tags, testCase.pipeline), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect ImageMirror mapping: %v", testCase.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}
