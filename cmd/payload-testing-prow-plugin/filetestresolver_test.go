package main

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestNewFileTestResolver(t *testing.T) {
	testCases := []struct {
		name          string
		dir           string
		expected      *fileTestResolver
		expectedError error
	}{
		{
			name: "basic case",
			dir:  filepath.Join("testdata", "config"),
			expected: &fileTestResolver{
				tuples: map[string]api.MetadataWithTest{
					"periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial": {
						Metadata: api.Metadata{
							Org:     "openshift",
							Repo:    "release",
							Branch:  "master",
							Variant: "nightly-4.10",
						},
						Test: "e2e-aws-serial",
					},
					"periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi": {
						Metadata: api.Metadata{
							Org:     "openshift",
							Repo:    "release",
							Branch:  "master",
							Variant: "nightly-4.10",
						},
						Test: "e2e-metal-ipi",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := newFileTestResolver(tc.dir)
			v, ok := actual.(*fileTestResolver)
			if !ok {
				t.Errorf("returned expected type")
			}
			if diff := cmp.Diff(tc.expected, v, cmp.Comparer(func(x, y fileTestResolver) bool {
				return cmp.Diff(x.tuples, y.tuples) == ""
			})); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
