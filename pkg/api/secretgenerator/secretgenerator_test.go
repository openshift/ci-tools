package secretgenerator

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestLoadConfigFromPath(t *testing.T) {
	testcases := []struct {
		name          string
		config        string
		expected      Config
		expectedError error
	}{
		{
			name: "no_parameters",
		},
		{
			name: "single parameter",
		},
		{
			name: "single parameter with multiple values",
		},
		{
			name: "two parameters with multiple values",
		},
		{
			name: "validation_command",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actualConfig, err := LoadConfigFromPath(filepath.Join("testdata", fmt.Sprintf("%s.yaml", t.Name())))
			if (tc.expectedError == nil) != (err == nil) {
				t.Fatalf("%s: expecting error \"%v\", got \"%v\"", t.Name(), tc.expectedError, err)
			} else if tc.expectedError != nil && err != nil && tc.expectedError.Error() != err.Error() {
				t.Fatalf("%s: expecting error msg %q, got %q", t.Name(), tc.expectedError.Error(), err.Error())
			} else if tc.expectedError == nil {
				if err != nil {
					t.Fatalf("didn't expect an error but got \"%v\"", err)
				}

				sort.Slice(actualConfig, func(p, q int) bool {
					return actualConfig[p].ItemName < actualConfig[q].ItemName
				})

				testhelper.CompareWithFixture(t, actualConfig)
			}
		})
	}
}
