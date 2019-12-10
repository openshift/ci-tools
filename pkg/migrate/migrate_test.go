package migrate

import (
	"reflect"
	"testing"
)

func TestMigrated(t *testing.T) {
	var testCases = []struct {
		testName string
		org      string
		repo     string
		branch   string
		expected bool
	}{
		{
			testName: "openshift/ci-secret-mirroring-controller/master is migrated",
			org:      "openshift",
			repo:     "ci-secret-mirroring-controller",
			branch:   "master",
			expected: true,
		},
		{
			testName: "openshift/installer/4.2 is NOT migrated",
			org:      "openshift",
			repo:     "installer",
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
