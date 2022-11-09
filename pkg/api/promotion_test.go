package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
					Disabled:  true,
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "origin",
				},
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotesOfficialImages(testCase.configSpec, WithOKD), testCase.expected; actual != expected {
				t.Errorf("%s: did not identify official promotion correctly, expected %v got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestGetGitTags(t *testing.T) {
	var testCases = []struct {
		name      string
		org       string
		repo      string
		commit    string
		expected  []string
		expectErr error
	}{
		{
			// If the tag is not stable, we could manage a tag in ci-tools
			name:     "the tag https://github.com/openshift/oc/releases/tag/openshift-clients-4.12.0-202208031327",
			org:      "openshift",
			repo:     "oc",
			commit:   "3c85519af6c4979c02ebb1886f45b366bbccbf55",
			expected: []string{"openshift-clients-4.12.0-202208031327"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Not really a unit test because the target function connects to github
			actual, actualErr := GetGitTags(testCase.org, testCase.repo, testCase.commit)
			if diff := cmp.Diff(testCase.expectErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
				return
			}
			if diff := cmp.Diff(testCase.expected, actual); actualErr == nil && diff != "" {
				t.Errorf("git tags differ from expected:\n%s", diff)
			}
		})
	}
}
