package main

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestFindSecretConfig(t *testing.T) {
	testCases := []struct {
		name          string
		secretName    string
		cluster       string
		secretConfigs []secretbootstrap.SecretConfig
		expected      *secretbootstrap.SecretConfig
		expectedError error
	}{
		{
			name:       "exists",
			secretName: "secret-a",
			cluster:    "cluster-1",
			secretConfigs: []secretbootstrap.SecretConfig{
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-0", Name: "secret-a"}}},
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}}},
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-b"}}},
			},
			expected: &secretbootstrap.SecretConfig{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}}},
		},
		{
			name:       "does not exist",
			secretName: "secret-c",
			cluster:    "cluster-1",
			secretConfigs: []secretbootstrap.SecretConfig{
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-0", Name: "secret-a"}}},
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}}},
				{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-b"}}},
			},
			expectedError: errors.New("couldn't find SecretConfig with name: secret-c and cluster: cluster-1"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secretConfig, err := findSecretConfig(tc.secretName, tc.cluster, tc.secretConfigs)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error did not match expectedError, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expected, secretConfig); diff != "" {
				t.Fatalf("secretConfig did not match expected, diff: %s", diff)
			}
		})
	}
}
