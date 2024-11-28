package secretbootstrap

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindSecret(t *testing.T) {
	for _, tc := range []struct {
		name             string
		secrets          []SecretConfig
		filters          []secretConfigFilter
		wantSecretConfig SecretConfig
		wantIdx          int
	}{
		{
			name: "Filter by cluster, namespace and name",
			filters: []secretConfigFilter{ByDestination(&SecretContext{
				Cluster:   "build05",
				Namespace: "ci",
				Name:      "manifest-tool-local-pusher",
			})},
			secrets: []SecretConfig{
				{
					From: map[string]ItemContext{
						".dockerconfigjson": {
							DockerConfigJSONData: []DockerConfigJSONData{{
								AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
								Item:        "build_farm",
								RegistryURL: "image-registry.openshift-image-registry.svc:5000",
							}},
						},
					},
					To: []SecretContext{{Cluster: "build05", Namespace: "ci", Name: "manifest-tool-local-pusher"}},
				},
				{
					From: map[string]ItemContext{
						"sa.deck.app.ci.config": {
							Field: "sa.deck.app.ci.config",
							Item:  "config-updater",
						},
					},
					To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "deck"}},
				},
			},
			wantSecretConfig: SecretConfig{
				From: map[string]ItemContext{
					".dockerconfigjson": {
						DockerConfigJSONData: []DockerConfigJSONData{{
							AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
							Item:        "build_farm",
							RegistryURL: "image-registry.openshift-image-registry.svc:5000",
						}},
					},
				},
				To: []SecretContext{{Cluster: "build05", Namespace: "ci", Name: "manifest-tool-local-pusher"}},
			},
			wantIdx: 0,
		},
		{
			name:    "Filter by cluster group",
			filters: []secretConfigFilter{ByDestination(&SecretContext{ClusterGroups: []string{"build_farm"}})},
			secrets: []SecretConfig{
				{
					From: map[string]ItemContext{
						".dockerconfigjson": {
							DockerConfigJSONData: []DockerConfigJSONData{{
								AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
								Item:        "build_farm",
								RegistryURL: "image-registry.openshift-image-registry.svc:5000",
							}},
						},
					},
					To: []SecretContext{{ClusterGroups: []string{"build_farm"}}},
				},
			},
			wantSecretConfig: SecretConfig{
				From: map[string]ItemContext{
					".dockerconfigjson": {
						DockerConfigJSONData: []DockerConfigJSONData{{
							AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
							Item:        "build_farm",
							RegistryURL: "image-registry.openshift-image-registry.svc:5000",
						}},
					},
				},
				To: []SecretContext{{ClusterGroups: []string{"build_farm"}}},
			},
			wantIdx: 0,
		},
		{
			name: "Filter by destination predicate",
			filters: []secretConfigFilter{ByDestinationFunc(func(sc *SecretContext) bool {
				return sc.Cluster == "build01"
			})},
			secrets: []SecretConfig{
				{
					From: map[string]ItemContext{
						".dockerconfigjson": {
							DockerConfigJSONData: []DockerConfigJSONData{{
								AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
								Item:        "build_farm",
								RegistryURL: "image-registry.openshift-image-registry.svc:5000",
							}},
						},
					},
					To: []SecretContext{{ClusterGroups: []string{"build_farm"}, Cluster: "build02"}},
				},
				{
					From: map[string]ItemContext{
						".dockerconfigjson": {
							DockerConfigJSONData: []DockerConfigJSONData{{
								AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
								Item:        "build_farm",
								RegistryURL: "image-registry.openshift-image-registry.svc:5000",
							}},
						},
					},
					To: []SecretContext{{ClusterGroups: []string{"build_farm"}, Cluster: "build01"}},
				},
			},
			wantSecretConfig: SecretConfig{
				From: map[string]ItemContext{
					".dockerconfigjson": {
						DockerConfigJSONData: []DockerConfigJSONData{{
							AuthField:   "token_image-pusher_build05_reg_auth_value.txt",
							Item:        "build_farm",
							RegistryURL: "image-registry.openshift-image-registry.svc:5000",
						}},
					},
				},
				To: []SecretContext{{ClusterGroups: []string{"build_farm"}, Cluster: "build01"}},
			},
			wantIdx: 1,
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()
			sc, i := FindSecret(tc.secrets, tc.filters...)
			if tc.wantIdx != i {
				tt.Errorf("want index %d but got %d", tc.wantIdx, i)
			}
			if diff := cmp.Diff(&tc.wantSecretConfig, sc); diff != "" {
				tt.Errorf("unexpected diff:\n%s", diff)
			}
		})
	}
}
