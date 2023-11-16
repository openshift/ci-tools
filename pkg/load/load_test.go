package load

import (
	"os"
	"reflect"
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestClusterProfilesConfig(t *testing.T) {
	var existingProfiles api.ClusterProfilesList
	for _, profileName := range api.ClusterProfiles() {
		existingProfiles = append(existingProfiles, api.ClusterProfileDetails{
			Profile: profileName,
		})
	}

	var profilesWithOwners api.ClusterProfilesList
	for _, profileName := range api.ClusterProfiles() {
		if profileName == "aws" {
			profilesWithOwners = append(profilesWithOwners, api.ClusterProfileDetails{
				Profile: profileName,
				Owners:  []api.ClusterProfileOwners{{Org: "org1"}},
			})
		} else if profileName == "aws-2" {
			profilesWithOwners = append(profilesWithOwners, api.ClusterProfileDetails{
				Profile: profileName,
				Owners:  []api.ClusterProfileOwners{{Org: "org2", Repos: []string{"repo1", "repo2"}}},
			})
		} else {
			profilesWithOwners = append(profilesWithOwners, api.ClusterProfileDetails{
				Profile: profileName,
			})
		}
	}

	var testCases = []struct {
		name     string
		expected api.ClusterProfilesList
		testYaml string
	}{
		{
			name:     "emptyOwnersFile",
			testYaml: ``,
			expected: existingProfiles,
		},
		{
			name: "profilesWithOwners",
			testYaml: `
        - profile: aws
          owners:
            - org: org1
        - profile: aws-2
          owners:
            - org: org2
              repos:
                - repo1
                - repo2
    `,
			expected: profilesWithOwners,
		},
		{
			name: "profilesButNoOwners",
			testYaml: `
        - profile: aws
        - profile: aws-2
    `,
			expected: existingProfiles,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "test-config-*.yaml")
			if err != nil {
				t.Errorf("Error creating temporary file: %v", err)
			}
			defer func(name string) {
				if err := os.Remove(name); err != nil {
					t.Fatalf("Failed to remove tmp file: %v", err)
				}
			}(tmpFile.Name())
			if _, err = tmpFile.WriteString(tc.testYaml); err != nil {
				t.Errorf("Error writing to temporary file: %v", err)
			}
			if err = tmpFile.Close(); err != nil {
				t.Fatalf("Failed to close tmp file: %v", err)
			}

			actual, _ := ClusterProfilesConfig(tmpFile.Name())
			if !reflect.DeepEqual(tc.expected, actual) {
				t.Errorf("\nExpected: %v, \nActual: %v", tc.expected, actual)
			}
		})
	}
}
