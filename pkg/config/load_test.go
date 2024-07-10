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
