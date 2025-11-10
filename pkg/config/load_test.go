package config

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestExtractRepoElementsFromPath(t *testing.T) {
	testCases := []struct {
		name          string
		path          string
		expected      *Info
		expectedError bool
	}{
		{
			name: "simple path parses fine",
			path: "./org/repo/org-repo-branch.yaml",
			expected: &Info{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "",
				},
				Filename: "./org/repo/org-repo-branch.yaml",
				OrgPath:  "org",
				RepoPath: "org/repo",
			},
			expectedError: false,
		},
		{
			name:          "empty path fails to parse",
			path:          "",
			expected:      nil,
			expectedError: true,
		},
		{
			name: "prefix to a valid path parses fine",
			path: "./something/crazy/org/repo/org-repo-branch.yaml",
			expected: &Info{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "",
				},
				Filename: "./something/crazy/org/repo/org-repo-branch.yaml",
				OrgPath:  "something/crazy/org",
				RepoPath: "something/crazy/org/repo",
			},
			expectedError: false,
		},
		{
			name:          "too few nested directories fails to parse",
			path:          "./repo/org-repo-branch.yaml",
			expected:      nil,
			expectedError: true,
		},
		{
			name: "path with variant parses fine",
			path: "./org/repo/org-repo-branch__variant.yaml",
			expected: &Info{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Filename: "./org/repo/org-repo-branch__variant.yaml",
				OrgPath:  "org",
				RepoPath: "org/repo",
			},
			expectedError: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			repoInfo, err := InfoFromPath(testCase.path)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if diff := cmp.Diff(repoInfo, testCase.expected); diff != "" {
				t.Errorf("%s: didn't get correct elements, diff: %v", testCase.name, diff)
			}
		})
	}
}

func TestValidateProwgenConfig(t *testing.T) {
	testCases := []struct {
		name     string
		pConfig  *Prowgen
		expected error
	}{
		{
			name: "valid",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:           "#slack-channel",
						JobStatesToReport: []prowv1.ProwJobState{"error"},
						ReportTemplate:    "some template",
						JobNames:          []string{"unit", "e2e"},
					},
					{
						Channel:           "#slack-channel",
						JobStatesToReport: []prowv1.ProwJobState{"success"},
						ReportTemplate:    "some other template",
						JobNames:          []string{"lint"},
					},
				},
			},
		},
		{
			name: "invalid, same job in multiple slack reporter configs",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:           "#slack-channel",
						JobStatesToReport: []prowv1.ProwJobState{"error"},
						ReportTemplate:    "some template",
						JobNames:          []string{"unit", "e2e"},
					},
					{
						Channel:           "#slack-channel",
						JobStatesToReport: []prowv1.ProwJobState{"success"},
						ReportTemplate:    "some other template",
						JobNames:          []string{"unit"},
					},
				},
			},
			expected: errors.New("job: unit exists in multiple slack_reporter_configs, it should only be in one"),
		},
		{
			name: "invalid regex patterns cause validation errors",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:         "#slack-channel",
						JobNamePatterns: []string{"[invalid"},
					},
					{
						Channel:         "#other-channel",
						JobNamePatterns: []string{"^valid.*"},
					},
				},
			},
			expected: errors.New("invalid regex pattern: [invalid, error: error parsing regexp: missing closing ]: `[invalid`"),
		},
		{
			name: "duplicate regex patterns cause validation errors",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:         "#slack-channel",
						JobNamePatterns: []string{"^unit.*"},
					},
					{
						Channel:         "#other-channel",
						JobNamePatterns: []string{"^unit.*"},
					},
				},
			},
			expected: errors.New("regex pattern: ^unit.* exists in multiple slack_reporter_configs, it should only be in one"),
		},
		{
			name: "valid regex patterns pass validation",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:         "#slack-channel",
						JobNamePatterns: []string{"^unit.*", "^e2e.*"},
					},
					{
						Channel:         "#other-channel",
						JobNamePatterns: []string{"^integration.*"},
					},
				},
			},
		},
		{
			name: "invalid excluded job patterns cause validation errors",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:             "#slack-channel",
						ExcludedJobPatterns: []string{"[invalid"},
					},
					{
						Channel:             "#other-channel",
						ExcludedJobPatterns: []string{"^valid.*"},
					},
				},
			},
			expected: errors.New("invalid excluded job pattern: [invalid, error: error parsing regexp: missing closing ]: `[invalid`"),
		},
		{
			name: "valid excluded job patterns pass validation",
			pConfig: &Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:             "#slack-channel",
						JobNamePatterns:     []string{".*"},
						ExcludedJobPatterns: []string{".*-skip$", "^nightly-.*"},
					},
					{
						Channel:             "#other-channel",
						JobNames:            []string{"unit", "e2e"},
						ExcludedJobPatterns: []string{".*-flaky$"},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := validateProwgenConfig(tc.pConfig)
			if diff := cmp.Diff(result, tc.expected, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %v", diff)
			}
		})
	}
}

func TestValidateProwgenSkipOperatorPresubmits(t *testing.T) {
	testCases := []struct {
		name     string
		pConfig  *Prowgen
		branch   string
		variant  string
		expected bool
	}{
		{
			name: "skipping operator presubmits, exactly match",
			pConfig: &Prowgen{
				SkipOperatorPresubmits: []SkipOperatorPresubmits{
					{
						Branch:  "main",
						Variant: "4.18",
					},
					{
						Branch:  "dev",
						Variant: "4.19",
					},
				},
			},
			branch:   "main",
			variant:  "4.18",
			expected: true,
		},
		{
			name: "generating operator presubmits, mismatch branches",
			pConfig: &Prowgen{
				SkipOperatorPresubmits: []SkipOperatorPresubmits{
					{
						Branch:  "dev",
						Variant: "4.18",
					},
				},
			},
			branch:   "main",
			variant:  "4.18",
			expected: false,
		},
		{
			name:     "skipping operator presubmits, empty values",
			pConfig:  &Prowgen{},
			branch:   "main",
			variant:  "4.19",
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.pConfig.SkipOperatorPresubmits != nil {
				skip := tc.pConfig.SkipPresubmits(tc.branch, tc.variant)
				if skip != tc.expected {
					t.Fatalf("result doesn't match, expected %v, received %v", tc.expected, skip)
				}
			}
		})
	}
}

func TestProwgen_MergeDefaults_SlackReporterConfigs(t *testing.T) {
	testCases := []struct {
		name     string
		base     Prowgen
		defaults Prowgen
		expected []SlackReporterConfig
	}{
		{
			name: "slack reporter configs are never merged from defaults",
			base: Prowgen{},
			defaults: Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:             "#test-channel",
						JobStatesToReport:   []prowv1.ProwJobState{"failure"},
						JobNamePatterns:     []string{".*"},
						ExcludedJobPatterns: []string{".*-skip$"},
					},
				},
			},
			expected: nil,
		},
		{
			name: "existing slack reporter configs are preserved unchanged",
			base: Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:           "#existing-channel",
						JobStatesToReport: []prowv1.ProwJobState{"error"},
						JobNames:          []string{"unit"},
					},
				},
			},
			defaults: Prowgen{
				SlackReporterConfigs: []SlackReporterConfig{
					{
						Channel:             "#default-channel",
						JobStatesToReport:   []prowv1.ProwJobState{"failure"},
						JobNamePatterns:     []string{".*"},
						ExcludedJobPatterns: []string{".*-skip$"},
					},
				},
			},
			expected: []SlackReporterConfig{
				{
					Channel:           "#existing-channel",
					JobStatesToReport: []prowv1.ProwJobState{"error"},
					JobNames:          []string{"unit"},
				},
			},
		},
		{
			name:     "empty base with empty defaults stays empty",
			base:     Prowgen{},
			defaults: Prowgen{},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.base.MergeDefaults(&tc.defaults)
			if diff := cmp.Diff(tc.base.SlackReporterConfigs, tc.expected); diff != "" {
				t.Fatalf("SlackReporterConfigs don't match expected, diff: %v", diff)
			}
		})
	}
}
