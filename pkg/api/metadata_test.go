package api

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/utils/diff"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestMetadata_IsComplete(t *testing.T) {
	testCases := []struct {
		name        string
		metadata    Metadata
		expectError string
	}{
		{
			name:     "All required members -> complete",
			metadata: Metadata{Org: "organization", Repo: "repository", Branch: "branch"},
		},
		{
			name:        "Missing org -> incomplete",
			metadata:    Metadata{Repo: "repository", Branch: "branch"},
			expectError: "missing item: organization",
		},
		{
			name:        "Missing repo -> incomplete",
			metadata:    Metadata{Org: "organization", Branch: "branch"},
			expectError: "missing item: repository",
		},
		{
			name:        "Missing branch -> incomplete",
			metadata:    Metadata{Org: "organization", Repo: "repository"},
			expectError: "missing item: branch",
		},
		{
			name:        "Everything missing -> incomplete",
			expectError: "missing items: branch, organization, repository",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.metadata.IsComplete()
			if err == nil && tc.expectError != "" {
				t.Errorf("%s: expected error '%s', got nil", tc.expectError, tc.name)
			}
			if err != nil {
				if tc.expectError == "" {
					t.Errorf("%s: unexpected error %s", tc.name, err.Error())
				} else if err.Error() != tc.expectError {
					t.Errorf("%s: expected error '%s', got '%s", tc.name, tc.expectError, err.Error())
				}
			}
		})
	}
}

func TestMetadata_Basename(t *testing.T) {
	testCases := []struct {
		name     string
		metadata *Metadata
		expected string
	}{
		{
			name: "simple path creates simple basename",
			metadata: &Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "",
			},
			expected: "org-repo-branch.yaml",
		},
		{
			name: "path with variant creates complex basename",
			metadata: &Metadata{
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
			if actual, expected := testCase.metadata.Basename(), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestMetadata_ConfigMapName(t *testing.T) {
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
			name:     "main branch goes to main configmap",
			branch:   "main",
			expected: "ci-operator-main-configs",
		},
		{
			name:     "openshift 3.6 branch goes to 3.x configmap",
			branch:   "openshift-3.6",
			expected: "ci-operator-3.x-configs",
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
			name:     "release 1.0 branch goes to 1.x configmap",
			branch:   "release-1.0",
			expected: "ci-operator-1.x-configs",
		},
		{
			name:     "release 2.5 branch goes to 2.x configmap",
			branch:   "release-2.5",
			expected: "ci-operator-2.x-configs",
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
			info := Metadata{Branch: testCase.branch}
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

func TestMetadata_JobName(t *testing.T) {
	prefix := "le"
	testName := "a-test"
	testCases := []struct {
		name     string
		metadata Metadata
		expected string
	}{
		{
			name:     "without variant",
			metadata: Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			expected: "le-ci-org-repo-branch-a-test",
		},
		{
			name:     "with variant",
			metadata: Metadata{Org: "gro", Repo: "oper", Branch: "hcnarb", Variant: "also"},
			expected: "le-ci-gro-oper-hcnarb-also-a-test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.metadata.JobName(prefix, testName)
			if actual != tc.expected {
				t.Errorf("%s: expected '%s', got '%s'", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestMetadata_TestName(t *testing.T) {
	testName := "a-test"
	testCases := []struct {
		name     string
		metadata Metadata
		expected string
	}{
		{
			name:     "without variant",
			metadata: Metadata{Org: "org", Repo: "repo", Branch: "branch"},
			expected: "a-test",
		},
		{
			name:     "with variant",
			metadata: Metadata{Org: "gro", Repo: "oper", Branch: "hcnarb", Variant: "also"},
			expected: "also-a-test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.metadata.TestName(testName)
			if actual != tc.expected {
				t.Errorf("%s: expected '%s', got '%s'", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestMetadata_TestNameFromJobName(t *testing.T) {
	testCases := []struct {
		name     string
		metadata Metadata
		jobName  string
		expected string
	}{{
		name:     "without variant",
		metadata: Metadata{Org: "org", Repo: "repo", Branch: "branch"},
		jobName:  "pull-ci-org-repo-branch-test1",
		expected: "test1",
	}, {
		name:     "with variant",
		metadata: Metadata{Org: "gro", Repo: "oper", Branch: "hcnarb", Variant: "also"},
		jobName:  "pull-ci-gro-oper-hcnarb-also-test2",
		expected: "test2",
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.metadata.TestNameFromJobName(tc.jobName, "pull")
			if actual != tc.expected {
				t.Errorf("%s: expected '%s', got '%s'", tc.name, tc.expected, actual)
			}
		})
	}
}

func TestFlavorForBranch(t *testing.T) {
	testCases := []struct {
		name     string
		branch   string
		expected string
	}{
		{
			name:     "master branch goes to master configmap",
			branch:   "master",
			expected: "master",
		},
		{
			name:     "enterprise 3.6 branch goes to 3.x configmap",
			branch:   "enterprise-3.6",
			expected: "3.x",
		},
		{
			name:     "openshift 3.6 branch goes to 3.x configmap",
			branch:   "openshift-3.6",
			expected: "3.x",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "3.x",
		},
		{
			name:     "enterprise 3.11 branch goes to 3.x configmap",
			branch:   "enterprise-3.11",
			expected: "3.x",
		},
		{
			name:     "openshift 3.11 branch goes to 3.x configmap",
			branch:   "openshift-3.11",
			expected: "3.x",
		},
		{
			name:     "release 3.11 branch goes to 3.x configmap",
			branch:   "release-3.11",
			expected: "3.x",
		},
		{
			name:     "knative release branch goes to misc configmap",
			branch:   "release-0.2",
			expected: "misc",
		},
		{
			name:     "azure release branch goes to misc configmap",
			branch:   "release-v1",
			expected: "misc",
		},
		{
			name:     "ansible dev branch goes to misc configmap",
			branch:   "devel-40",
			expected: "misc",
		},
		{
			name:     "release 4.0 branch goes to 4.0 configmap",
			branch:   "release-4.0",
			expected: "4.0",
		},
		{
			name:     "release 4.1 branch goes to 4.1 configmap",
			branch:   "release-4.1",
			expected: "4.1",
		},
		{
			name:     "release 4.2 branch goes to 4.2 configmap",
			branch:   "release-4.2",
			expected: "4.2",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.expected, func(t *testing.T) {
			if actual, expected := FlavorForBranch(testCase.branch), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct basename: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}

func TestMetadataTestFromString(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    *MetadataWithTest
		expectedErr error
	}{
		{
			name:  "happy case",
			input: "org/repo@branch:test",
			expected: &MetadataWithTest{
				Metadata: Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Test:     "test",
			},
		},
		{
			name:  "happy case with variant",
			input: "orgz/repoz@branchz__variantz:testz",
			expected: &MetadataWithTest{
				Metadata: Metadata{Org: "orgz", Repo: "repoz", Branch: "branchz", Variant: "variantz"},
				Test:     "testz",
			},
		},
		{
			name:        "missing org",
			input:       "repoz@branchz__variantz:testz",
			expectedErr: errors.New("test path not in org/repo@branch:test or org/repo@branch__variant:test format: repoz@branchz__variantz:testz"),
		},
		{
			name:        "missing branch",
			input:       "o/r@:t",
			expectedErr: errors.New("test path not in org/repo@branch:test or org/repo@branch__variant:test format: o/r@:t"),
		},
		{
			name:        "missing branch with variant",
			input:       "o/r@__v:t",
			expectedErr: errors.New("test path not in org/repo@branch:test or org/repo@branch__variant:test format: o/r@__v:t"),
		},
		{
			name:        "missing test",
			input:       "o/r@b",
			expectedErr: errors.New("test path not in org/repo@branch:test or org/repo@branch__variant:test format: o/r@b"),
		},
		{
			name:        "missing test with variant",
			input:       "o/r@b__v",
			expectedErr: errors.New("test path not in org/repo@branch:test or org/repo@branch__variant:test format: o/r@b__v"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := MetadataTestFromString(tc.input)
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expected, actual); tc.expectedErr == nil && diff != "" {
				t.Errorf("result differs from expected:\n%s", diff)
			}
		})
	}
}
