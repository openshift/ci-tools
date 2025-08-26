package gsmsecrets

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetDesiredState(t *testing.T) {
	config := Config{
		ProjectIdString: "test-project",
		ProjectIdNumber: "123456789",
	}
	testCases := []struct {
		name       string
		configFile string
	}{
		{
			name:       "basic config",
			configFile: "testdata/basic-config.yaml",
		},
		{
			name:       "no secret collections",
			configFile: "testdata/no-secret-collections.yaml",
		},
		{
			name:       "one secret collection",
			configFile: "testdata/one-secret-collection.yaml",
		},
		{
			name:       "complex config",
			configFile: "testdata/complex-config.yaml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceAccounts, secrets, bindings, collections, err := GetDesiredState(tc.configFile, config)
			if err != nil {
				t.Fatalf("GetDesiredState() failed: %v", err)
			}

			testhelper.CompareWithFixture(t, serviceAccounts, testhelper.WithPrefix("sa-"))
			testhelper.CompareWithFixture(t, secrets, testhelper.WithPrefix("secrets-"))
			testhelper.CompareWithFixture(t, bindings, testhelper.WithPrefix("bindings-"))
			testhelper.CompareWithFixture(t, collections, testhelper.WithPrefix("collections-"))
		})
	}
}
