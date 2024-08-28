package cisecretbootstrap

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestUpdateSecret(t *testing.T) {
	testCases := []struct {
		name string
		Options
		secretGenerator func(Options) secretbootstrap.SecretConfig
		config          secretbootstrap.Config
		expectedConfig  secretbootstrap.Config
	}{
		{
			name: "secret does not exist",
			Options: Options{
				ClusterName: "newCluster",
			},
			secretGenerator: func(o Options) secretbootstrap.SecretConfig {
				return secretbootstrap.SecretConfig{
					From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
					To:   []secretbootstrap.SecretContext{{Cluster: "newCluster", Name: "secret-a"}},
				}
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "existingCluster", Name: "secret-a"}},
					},
				},
			},
			expectedConfig: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "existingCluster", Name: "secret-a"}},
					},
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "newCluster", Name: "secret-a"}},
					},
				},
			},
		},
		{
			name: "secret exists",
			Options: Options{
				ClusterName: "existingCluster",
			},
			secretGenerator: func(o Options) secretbootstrap.SecretConfig {
				return secretbootstrap.SecretConfig{
					From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
					To:   []secretbootstrap.SecretContext{{Cluster: "existingCluster", Name: "secret-a"}},
				}
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}},
					},
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "existingCluster", Name: "secret-a"}},
					},
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "cluster-2", Name: "secret-a"}},
					},
				},
			},
			expectedConfig: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}},
					},
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "existingCluster", Name: "secret-a"}},
					},
					{
						From: map[string]secretbootstrap.ItemContext{"item": {Item: "item-a"}},
						To:   []secretbootstrap.SecretContext{{Cluster: "cluster-2", Name: "secret-a"}},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := updateSecret(tc.secretGenerator)(&tc.config, tc.Options); err != nil {
				t.Fatalf("received error: %v", err)
			}
			if diff := cmp.Diff(tc.expectedConfig, tc.config); diff != "" {
				t.Fatalf("config did not match expected, diff: %s", diff)
			}
		})
	}
}

func TestFindSecretConfig(t *testing.T) {
	testCases := []struct {
		name           string
		secretName     string
		cluster        string
		secretConfigs  []secretbootstrap.SecretConfig
		expectedConfig *secretbootstrap.SecretConfig
		expectedIndex  int
		expectedError  error
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
			expectedConfig: &secretbootstrap.SecretConfig{To: []secretbootstrap.SecretContext{{Cluster: "cluster-1", Name: "secret-a"}}},
			expectedIndex:  1,
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
			expectedIndex: -1,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			idx, secretConfig, err := findSecretConfig(tc.secretName, tc.cluster, tc.secretConfigs)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error did not match expectedError, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedConfig, secretConfig); diff != "" {
				t.Fatalf("secretConfig did not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedIndex, idx); diff != "" {
				t.Fatalf("index did not match expected, diff: %s", diff)
			}
		})
	}
}
