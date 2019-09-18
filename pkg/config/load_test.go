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
		expected      *Info
		expectedError bool
	}{
		{
			name: "simple path parses fine",
			path: "./org/repo/org-repo-branch.yaml",
			expected: &Info{
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "",
				Filename: "./org/repo/org-repo-branch.yaml",
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
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "",
				Filename: "./something/crazy/org/repo/org-repo-branch.yaml",
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
				Org:      "org",
				Repo:     "repo",
				Branch:   "branch",
				Variant:  "variant",
				Filename: "./org/repo/org-repo-branch__variant.yaml",
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
			if actual, expected := repoInfo, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct elements: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestInfo_Basename(t *testing.T) {
	testCases := []struct {
		name     string
		info     *Info
		expected string
	}{
		{
			name: "simple path creates simple basename",
			info: &Info{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "",
			},
			expected: "org-repo-branch.yaml",
		},
		{
			name: "path with variant creates complex basename",
			info: &Info{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			expected: "org-repo-branch__variant.yaml",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			if actual, expected := testCase.info.Basename(), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestInfo_ConfigMapName(t *testing.T) {
	testCases := []struct {
		name     string
		branch   string
		expected string
	}{
		{
			name:     "master branch goes to master configmap",
			branch:   "master",
			expected: "ci-operator-master-configs",
		},
		{
			name:     "enterprise 3.6 branch goes to 3.x configmap",
			branch:   "enterprise-3.6",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "openshift 3.6 branch goes to 3.x configmap",
			branch:   "openshift-3.6",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "enterprise 3.11 branch goes to 3.x configmap",
			branch:   "enterprise-3.11",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "openshift 3.11 branch goes to 3.x configmap",
			branch:   "openshift-3.11",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "ci-operator-3.x-configs",
		},
		{
			name:     "knative release branch goes to misc configmap",
			branch:   "release-0.2",
			expected: "ci-operator-misc-configs",
		},
		{
			name:     "azure release branch goes to misc configmap",
			branch:   "release-v1",
			expected: "ci-operator-misc-configs",
		},
		{
			name:     "ansible dev branch goes to misc configmap",
			branch:   "devel-40",
			expected: "ci-operator-misc-configs",
		},
		{
			name:     "release 4.0 branch goes to 4.0 configmap",
			branch:   "release-4.0",
			expected: "ci-operator-4.0-configs",
		},
		{
			name:     "release 4.1 branch goes to 4.1 configmap",
			branch:   "release-4.1",
			expected: "ci-operator-4.1-configs",
		},
		{
			name:     "release 4.2 branch goes to 4.2 configmap",
			branch:   "release-4.2",
			expected: "ci-operator-4.2-configs",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			info := Info{Branch: testCase.branch}
			actual, expected := info.ConfigMapName(), testCase.expected
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
			// test that ConfigMapName() stays in sync with IsCiopConfigCM()
			if !IsCiopConfigCM(actual) {
				t.Errorf("%s: IsCiopConfigCM() returned false for %s", testCase.name, actual)
			}
		})
	}
}
