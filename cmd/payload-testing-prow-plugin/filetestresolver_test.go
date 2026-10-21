package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestFileTestResolver(t *testing.T) {
	testCases := []struct {
		name      string
		dir       string
		verifyFun func(testResolver testResolver) error
		org       string
		job       string
		shards    int
		wantError bool
	}{
		{
			name:   "openshift periodic",
			org:    "openshift",
			job:    "periodic-ci-openshift-ci-main-nightly-e2e",
			shards: 1,
		},
		{
			name:   "openshift-eng periodic",
			org:    "openshift-eng",
			job:    "periodic-ci-openshift-eng-ci-main-nightly-e2e",
			shards: 1,
		},
		{
			name:   "shiftstack periodic",
			org:    "shiftstack",
			job:    "periodic-ci-shiftstack-ci-main-nightly-e2e",
			shards: 1,
		},
		{
			name:   "shiftstack sharded periodic",
			org:    "shiftstack",
			job:    "periodic-ci-shiftstack-ci-main-nightly-e2e",
			shards: 3,
		},
		{
			name:      "unsupported organization with matching periodic",
			org:       "unrelated",
			job:       "periodic-ci-unrelated-ci-main-nightly-e2e",
			wantError: true,
		},
		{
			name:      "shiftstack unknown job",
			org:       "shiftstack",
			job:       "periodic-ci-shiftstack-ci-main-nightly-unknown",
			wantError: true,
		},
		{
			name:      "shiftstack nonperiodic test",
			org:       "shiftstack",
			job:       "periodic-ci-shiftstack-ci-main-nightly-presubmit",
			wantError: true,
		},
		{
			name: "basic case",
			dir:  filepath.Join("testdata", "config"),
			verifyFun: func(testResolver testResolver) error {
				actual, shardCount, actualErr := testResolver.resolve("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial")
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
				if shardCount != 1 {
					return fmt.Errorf("shardCount: got %d, want 1", shardCount)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, shardCount, actualErr = testResolver.resolve("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi")
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
				if shardCount != 1 {
					return fmt.Errorf("shardCount: got %d, want 1", shardCount)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, shardCount, actualErr = testResolver.resolve("periodic-ci-openshift-api-master-build")
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
				if shardCount != 1 {
					return fmt.Errorf("shardCount: got %d, want 1", shardCount)
				}
				if diff := cmp.Diff(nil, actualErr, testhelper.EquateErrorMessage); diff != "" {
					return fmt.Errorf("actualErr differs from expected: %s", diff)
				}

				actual, shardCount, actualErr = testResolver.resolve("some-job")
				expected = api.MetadataWithTest{}
				if diff := cmp.Diff(expected, actual); diff != "" {
					return fmt.Errorf("actual differs from expected: %s", diff)
				}
				if shardCount != 0 {
					return fmt.Errorf("shardCount on error: got %d, want 0", shardCount)
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
			if tc.org != "" {
				metadata := api.Metadata{Org: tc.org, Repo: "ci", Branch: "main", Variant: "nightly"}
				periodic := api.TestStepConfiguration{As: "e2e", Cron: new("0 0 * * *")}
				if tc.shards > 1 {
					periodic.ShardCount = new(tc.shards)
				}
				configAgent := agents.NewFakeConfigAgent(config.ByOrgRepo{
					tc.org: {"ci": {{
						Metadata: metadata,
						Tests:    []api.TestStepConfiguration{periodic, {As: "presubmit"}},
					}}},
				})
				resolver := &fileTestResolver{configAgent: configAgent}
				actual, shards, err := resolver.resolve(tc.job)
				want := api.MetadataWithTest{Metadata: metadata, Test: "e2e"}
				var wantErr error
				if tc.wantError {
					want = api.MetadataWithTest{}
					wantErr = fmt.Errorf("failed to resolve job %s", tc.job)
				}
				testhelper.Diff(t, "resolved test", want, actual)
				testhelper.Diff(t, "shard count", tc.shards, shards)
				testhelper.Diff(t, "error", wantErr, err, testhelper.EquateErrorMessage)
				return
			}
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
