package promotion

import (
	"reflect"
	"testing"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *cioperatorapi.ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "ocp",
					Disabled:  true,
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd release imagestream in okd namespace produces official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "openshift",
					Name:      "origin-v4.0",
				},
			},
			expected: true,
		},
		{
			name: "config explicitly promoting to random imagestream in okd namespace does not produce official images",
			configSpec: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Namespace: "openshift",
					Name:      "random",
				},
			},
			expected: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotesOfficialImages(testCase.configSpec), testCase.expected; actual != expected {
				t.Errorf("%s: did not identify official promotion correctly, expected %v got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestDetermineReleaseBranches(t *testing.T) {
	var testCases = []struct {
		name                                         string
		currentRelease, futureRelease, currentBranch string
		expectedFutureBranch                         string
		expectedError                                bool
	}{
		{
			name:                 "promotion from weird branch errors",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "weird",
			expectedFutureBranch: "",
			expectedError:        true,
		},
		{
			name:                 "promotion from master makes a release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "master",
			expectedFutureBranch: "release-4.1",
			expectedError:        false,
		},
		{
			name:                 "promotion from openshift release branch makes a new release branch",
			currentRelease:       "4.0",
			futureRelease:        "4.1",
			currentBranch:        "openshift-4.0",
			expectedFutureBranch: "openshift-4.1",
			expectedError:        false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actualFutureBranch, err := DetermineReleaseBranch(testCase.currentRelease, testCase.futureRelease, testCase.currentBranch)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if actual, expected := actualFutureBranch, testCase.expectedFutureBranch; actual != expected {
				t.Errorf("%s: incorrect future branch, expected %q, got %q", testCase.name, expected, actual)
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
