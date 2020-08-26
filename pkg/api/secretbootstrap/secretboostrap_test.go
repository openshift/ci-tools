package secretbootstrap

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
)

func TestResolving(t *testing.T) {
	testCases := []struct {
		name           string
		config         Config
		expectedError  string
		expectedConfig Config
	}{
		{
			name:          "Both cluster and cluster_groups set, error",
			config:        Config{Secrets: []SecretConfig{{To: []SecretContext{{Cluster: "cl", ClusterGroups: []string{"a"}}}}}},
			expectedError: "item secrets.0.to.0 has both cluster and cluster_groups set, those are mutually exclusive",
		},
		{
			name: "Cluster groups get resolved",
			config: Config{
				ClusterGroups: map[string][]string{
					"group-a": {"a"},
					"group-b": {"b"},
				},
				Secrets: []SecretConfig{{
					To: []SecretContext{{
						ClusterGroups: []string{"group-a", "group-b"},
						Namespace:     "namspace",
						Name:          "name",
						Type:          corev1.SecretTypeBasicAuth,
					}},
				}},
			},
			expectedConfig: Config{
				ClusterGroups: map[string][]string{
					"group-a": {"a"},
					"group-b": {"b"},
				},
				Secrets: []SecretConfig{{
					To: []SecretContext{
						{
							Cluster:   "a",
							Namespace: "namspace",
							Name:      "name",
							Type:      corev1.SecretTypeBasicAuth,
						},
						{
							Cluster:   "b",
							Namespace: "namspace",
							Name:      "name",
							Type:      corev1.SecretTypeBasicAuth,
						},
					},
				}},
			},
		},
		{
			name:          "Inexistent ClusterGroups, error",
			config:        Config{Secrets: []SecretConfig{{To: []SecretContext{{ClusterGroups: []string{"a"}}}}}},
			expectedError: "item secrets.0.to.0 references inexistent cluster_group a",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var errMsg string
			err := tc.config.resolve()
			if err != nil {
				errMsg = err.Error()
			}
			if errMsg != tc.expectedError {
				t.Fatalf("got error %s, expected error %s", errMsg, tc.expectedError)
			}
			if err != nil {
				return
			}

			if diff := cmp.Diff(tc.expectedConfig, tc.config); diff != "" {
				t.Errorf("expected config differs from actual config: %s", diff)
			}
		})
	}
}
