package migrate

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestMigrated(t *testing.T) {
	migratedRepos = sets.NewString(
		"openshift/origin/master",
	)
	var testCases = []struct {
		testName string
		org      string
		repo     string
		branch   string
		expected bool
	}{
		{
			testName: "openshift/origin/master is migrated",
			org:      "openshift",
			repo:     "origin",
			branch:   "master",
			expected: true,
		},
		{
			testName: "openshift/some-repo/4.2 is NOT migrated",
			org:      "openshift",
			repo:     "some-repo",
			branch:   "4.2",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			if actual, expected := Migrated(testCase.org, testCase.repo, testCase.branch), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect result '%t', expecting '%t'", testCase.testName, actual, expected)
			}
		})
	}
}
