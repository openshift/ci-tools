package onboard

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestFindSecretItem(t *testing.T) {
	secretA := secretgenerator.SecretItem{
		ItemName: buildUFarm,
		Fields: []secretgenerator.FieldGenerator{{
			Name: "secret-a",
			Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
		}},
		Params: map[string][]string{
			"cluster": {
				string(api.ClusterAPPCI),
				string(api.ClusterBuild01)}},
	}
	config := SecretGenConfig{
		{
			ItemName: "release-controller",
			Fields: []secretgenerator.FieldGenerator{{
				Name: "secret-0",
				Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
			}},
			Params: map[string][]string{
				"cluster": {
					string(api.ClusterAPPCI),
					string(api.ClusterBuild01)}},
		},
		secretA,
		{
			ItemName: buildUFarm,
			Fields: []secretgenerator.FieldGenerator{{
				Name: "secret-b",
				Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
			}},
			Params: map[string][]string{
				"cluster": {
					string(api.ClusterAPPCI),
					string(api.ClusterBuild02)}},
		},
	}
	type args struct {
		itemName    string
		name        string
		likeCluster string
		c           SecretGenConfig
	}
	testCases := []struct {
		name          string
		args          args
		expected      *secretgenerator.SecretItem
		expectedError error
	}{
		{
			name: "existing",
			args: args{
				itemName:    buildUFarm,
				name:        "secret-a",
				likeCluster: string(api.ClusterBuild01),
				c:           config,
			},
			expected: &secretA,
		},
		{
			name: "non-existing",
			args: args{
				itemName:    buildUFarm,
				name:        "secret-c",
				likeCluster: string(api.ClusterBuild01),
				c:           config,
			},
			expected:      nil,
			expectedError: errors.New("couldn't find SecretItem with item_name: build_farm name: secret-c containing cluster: build01"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secretItem, err := findSecretItem(tc.args.itemName, tc.args.name, tc.args.likeCluster, tc.args.c)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error: %v - expectedError: %v", err, tc.expectedError)
				return
			}
			if diff := cmp.Diff(tc.expected, secretItem); diff != "" {
				t.Fatalf("wrong secretItem returned. Diff: %s", diff)
			}
		})
	}
}

func TestUpdateSecretGeneratorConfig(t *testing.T) {
	serviceAccountConfigPath := serviceAccountKubeconfigPath(serviceAccountWildcard, clusterWildcard)
	testCases := []struct {
		name string
		options
		input    SecretGenConfig
		expected SecretGenConfig
	}{
		{
			name: "basic",
			options: options{
				clusterName: "newcluster",
			},
			input: SecretGenConfig{
				{
					ItemName: buildUFarm,
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01)}},
				},
				{
					ItemName: buildUFarm,
					Fields: []secretgenerator.FieldGenerator{{
						Name: fmt.Sprintf("token_%s_%s_reg_auth_value.txt", serviceAccountWildcard, clusterWildcard),
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01)},
						"service_account": {
							"image-puller",
						},
					},
				},
				{
					ItemName: "ci-chat-bot",
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01)}},
				},
				{
					ItemName: podScaler,
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01)}},
				},
			},
			expected: SecretGenConfig{
				{
					ItemName: buildUFarm,
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01),
							"newcluster"}},
				},
				{
					ItemName: buildUFarm,
					Fields: []secretgenerator.FieldGenerator{{
						Name: fmt.Sprintf("token_%s_%s_reg_auth_value.txt", serviceAccountWildcard, clusterWildcard),
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01),
							"newcluster",
						},
						"service_account": {
							"image-puller",
						},
					},
				},
				{
					ItemName: "ci-chat-bot",
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01),
							"newcluster"}},
				},
				{
					ItemName: podScaler,
					Fields: []secretgenerator.FieldGenerator{{
						Name: serviceAccountConfigPath,
						Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
					}},
					Params: map[string][]string{
						"cluster": {
							string(api.ClusterAPPCI),
							string(api.ClusterBuild01),
							"newcluster"}},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := updateSecretGeneratorConfig(tc.options, &tc.input); err != nil {
				t.Fatalf("error received while updating secret generator config: %v", err)
			}
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Fatalf("expected config was different than results: %s", diff)
			}
		})
	}
}
