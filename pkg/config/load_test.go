package config

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestExtractRepoElementsFromPath(t *testing.T) {
	testCases := []struct {
		name          string
		path          string
		expected      *FilePathElements
		expectedError bool
	}{
		{
			name: "simple path parses fine",
			path: "./org/repo/org-repo-branch.yaml",
			expected: &FilePathElements{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "",
				Filename: "org-repo-branch.yaml",
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
			expected: &FilePathElements{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "",
				Filename: "org-repo-branch.yaml",
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
			expected: &FilePathElements{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "variant",
				Filename: "org-repo-branch__variant.yaml",
			},
			expectedError: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			repoInfo, err := extractRepoElementsFromPath(testCase.path)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if actual, expected := repoInfo, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct elements: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}
