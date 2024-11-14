package rehearse

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	prowplugins "sigs.k8s.io/prow/pkg/plugins"
)

func TestNewConfigMaps(t *testing.T) {
	cuCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			"path/to/a/template.yaml": {
				Name: "a-template-configmap",
			},
			"path/to/a/cluster-profile/*.yaml": {
				Name: "a-cluster-profile-configmap",
			},
		},
	}

	testCases := []struct {
		description string
		paths       []string

		expectCMS   ConfigMaps
		expectError error
	}{
		{
			description: "no paths",
		},
		{
			description: "paths not hitting any configured pattern",
			paths: []string{
				"path/not/covered/by/any/pattern.yaml",
			},
			expectError: fmt.Errorf("path not covered by any config-updater pattern: path/not/covered/by/any/pattern.yaml"),
		},
		{
			description: "path hitting a pattern",
			paths: []string{
				"path/to/a/template.yaml",
			},
			expectCMS: ConfigMaps{
				Paths:           sets.New[string]("path/to/a/template.yaml"),
				Names:           map[string]string{"a-template-configmap": "rehearse-1234-SOMESHA-test-a-template-configmap"},
				ProductionNames: sets.New[string]("a-template-configmap"),
				Patterns:        sets.New[string]("path/to/a/template.yaml"),
			},
		},
		{
			description: "multiple paths hitting one pattern",
			paths: []string{
				"path/to/a/cluster-profile/vars.yaml",
				"path/to/a/cluster-profile/vars-origin.yaml",
			},
			expectCMS: ConfigMaps{
				Paths:           sets.New[string]("path/to/a/cluster-profile/vars.yaml", "path/to/a/cluster-profile/vars-origin.yaml"),
				Names:           map[string]string{"a-cluster-profile-configmap": "rehearse-1234-SOMESHA-test-a-cluster-profile-configmap"},
				ProductionNames: sets.New[string]("a-cluster-profile-configmap"),
				Patterns:        sets.New[string]("path/to/a/cluster-profile/*.yaml"),
			},
		},
		{
			description: "multiple paths hitting multiple patterns",
			paths: []string{
				"path/to/a/cluster-profile/vars.yaml",
				"path/to/a/cluster-profile/vars-origin.yaml",
				"path/to/a/template.yaml",
			},
			expectCMS: ConfigMaps{
				Paths: sets.New[string](
					"path/to/a/cluster-profile/vars.yaml",
					"path/to/a/cluster-profile/vars-origin.yaml",
					"path/to/a/template.yaml",
				),
				Names: map[string]string{
					"a-cluster-profile-configmap": "rehearse-1234-SOMESHA-test-a-cluster-profile-configmap",
					"a-template-configmap":        "rehearse-1234-SOMESHA-test-a-template-configmap",
				},
				ProductionNames: sets.New[string]("a-cluster-profile-configmap", "a-template-configmap"),
				Patterns:        sets.New[string]("path/to/a/cluster-profile/*.yaml", "path/to/a/template.yaml"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(*testing.T) {
			cms, err := NewConfigMaps(tc.paths, "test", "SOMESHA", 1234, cuCfg)

			if (tc.expectError == nil) != (err == nil) {
				t.Fatalf("Did not return error as expected:\n%s", cmp.Diff(tc.expectError, err))
			} else if tc.expectError != nil && err != nil && tc.expectError.Error() != err.Error() {
				t.Fatalf("Expected different error:\n%s", cmp.Diff(tc.expectError.Error(), err.Error()))
			}

			if err == nil {
				if diffCms := cmp.Diff(tc.expectCMS, cms); diffCms != "" {
					t.Errorf("Output differs from expected:\n%s", diffCms)
				}
			}
		})
	}
}
