package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestFileTestResolver(t *testing.T) {
	testCases := []struct {
		name      string
		dir       string
		verifyFun func(testResolver testResolver) error
	}{
		{
			name: "basic case",
			dir:  filepath.Join("testdata", "config"),
			verifyFun: func(testResolver testResolver) error {
				actual, actualErr := testResolver.resolve("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial")
				expected := api.MetadataWithTest{Metadata: api.Metadata{
					Org:     "openshift",
					Repo:    "release",
					Branch:  "master",
					Variant: "nightly-4.10",
				},
					Test: "e2e-aws-serial",
				}
				if diff := cmp.Diff(expected, actual); diff != "" {
					return fmt.Errorf("actual differs from expected: %s", diff)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, actualErr = testResolver.resolve("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi")
				expected = api.MetadataWithTest{Metadata: api.Metadata{
					Org:     "openshift",
					Repo:    "release",
					Branch:  "master",
					Variant: "nightly-4.10",
				},
					Test: "e2e-metal-ipi",
				}
				if diff := cmp.Diff(expected, actual); diff != "" {
					return fmt.Errorf("actual differs from expected: %s", diff)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, actualErr = testResolver.resolve("periodic-ci-openshift-api-master-build")
				expected = api.MetadataWithTest{Metadata: api.Metadata{
					Org:    "openshift",
					Repo:   "api",
					Branch: "master",
				},
					Test: "build",
				}
				if diff := cmp.Diff(expected, actual); diff != "" {
					return fmt.Errorf("actual differs from expected: %s", diff)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, actualErr = testResolver.resolve("some-job")
				expected = api.MetadataWithTest{}
				if diff := cmp.Diff(expected, actual); diff != "" {
					return fmt.Errorf("actual differs from expected: %s", diff)
				}
				if diff := cmp.Diff(fmt.Errorf("failed to resolve job some-job"), actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				return nil
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configAgent, err := agents.NewConfigAgent(tc.dir, nil)
			if err != nil {
				t.Fatalf("Failed to get config agent: %v", err)
			}
			if tc.verifyFun != nil {
				if err := tc.verifyFun(&fileTestResolver{configAgent: configAgent}); err != nil {
					t.Errorf("unexpected error occurred: %v", err)
				}
			}
		})
	}
}
