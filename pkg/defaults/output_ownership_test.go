package defaults

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/validation"
)

func TestMergedImageOutputOwnership(t *testing.T) {
	t.Parallel()
	borrowed := api.ProjectDirectoryImageBuildStepConfiguration{To: "test-image"}
	pr := api.ProjectDirectoryImageBuildStepConfiguration{To: "test-image-openshift.installer", Ref: "openshift.installer"}
	otherPR := api.ProjectDirectoryImageBuildStepConfiguration{To: "test-image-openshift.other", Ref: "openshift.other"}
	for _, testCase := range []struct {
		name      string
		images    []api.ProjectDirectoryImageBuildStepConfiguration
		outputs   map[string][]api.PipelineImageStreamTagReference
		wantError bool
	}{
		{name: "standalone", images: []api.ProjectDirectoryImageBuildStepConfiguration{borrowed}, outputs: map[string][]api.PipelineImageStreamTagReference{"test-image": {"test-image"}}},
		{name: "borrowed first", images: []api.ProjectDirectoryImageBuildStepConfiguration{borrowed, pr}, outputs: map[string][]api.PipelineImageStreamTagReference{"test-image": {pr.To}}},
		{name: "PR first", images: []api.ProjectDirectoryImageBuildStepConfiguration{pr, borrowed}, outputs: map[string][]api.PipelineImageStreamTagReference{"test-image": {pr.To}}},
		{name: "distinct outputs preserved", images: []api.ProjectDirectoryImageBuildStepConfiguration{{To: "borrowed-test"}, pr}, outputs: map[string][]api.PipelineImageStreamTagReference{"borrowed-test": {"borrowed-test"}, "test-image": {pr.To}}},
		{name: "conflicting PR outputs remain invalid", images: []api.ProjectDirectoryImageBuildStepConfiguration{pr, otherPR}, outputs: map[string][]api.PipelineImageStreamTagReference{"test-image": {pr.To, otherPR.To}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := &api.ReleaseBuildConfiguration{Images: api.ImageConfiguration{Items: testCase.images}}
			graph := FromConfigStatic(config)
			outputs := make(map[string][]api.PipelineImageStreamTagReference)
			var builds []api.ProjectDirectoryImageBuildStepConfiguration
			for _, raw := range graph.Steps {
				if image := raw.ProjectDirectoryImageBuildStepConfiguration; image != nil {
					builds = append(builds, *image)
				}
				if output := raw.OutputImageTagStepConfiguration; output != nil {
					outputs[output.To.Tag] = append(outputs[output.To.Tag], output.From)
				}
			}
			if diff := cmp.Diff(testCase.images, builds, cmp.AllowUnexported(api.ProjectDirectoryImageBuildStepConfiguration{})); diff != "" {
				t.Errorf("pipeline build definitions changed: %s", diff)
			}
			if diff := cmp.Diff(testCase.outputs, outputs); diff != "" {
				t.Errorf("output ownership: %s", diff)
			}
			if err := validation.IsValidGraphConfiguration(graph.Steps); (err != nil) != testCase.wantError {
				t.Errorf("unexpected graph validation: %v", err)
			}
		})
	}
}
