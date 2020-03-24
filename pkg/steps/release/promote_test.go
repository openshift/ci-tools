package release

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/diff"

	"github.com/openshift/ci-tools/pkg/api"
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
			name: "enabled config with prefix returns prefixed input list",
			config: api.PromotionConfiguration{
				Disabled:   false,
				NamePrefix: "some",
			},
			images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: api.PipelineImageStreamTagReference("foo")},
				{To: api.PipelineImageStreamTagReference("bar")},
				{To: api.PipelineImageStreamTagReference("baz")},
			},
			requiredImages:   sets.NewString(),
			expectedBySource: map[string]string{"somefoo": "foo", "somebar": "bar", "somebaz": "baz"},
			expectedNames:    sets.NewString("somefoo", "somebar", "somebaz"),
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
			bySource, names := ToPromote(test.config, test.images, test.requiredImages)
			if actual, expected := bySource, test.expectedBySource; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect tags by source: %s", test.name, diff.ObjectDiff(actual, expected))
			}

			if actual, expected := names, test.expectedNames; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect names: %s", test.name, diff.ObjectDiff(actual, expected))
			}
		})
	}
}
