package secretbootstrap

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/testhelper"
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
		{
			name: "DPTP prefix gets added to normal BW items",
			config: Config{
				VaultDPTPPRefix: "prefix",
				Secrets: []SecretConfig{{
					From: map[string]ItemContext{"...": {Item: "foo", Field: "bar"}},
					To: []SecretContext{{
						Cluster:   "foo",
						Namespace: "namspace",
						Name:      "name",
						Type:      corev1.SecretTypeBasicAuth,
					}},
				}},
			},
			expectedConfig: Config{
				VaultDPTPPRefix: "prefix",
				Secrets: []SecretConfig{{
					From: map[string]ItemContext{"...": {Item: "prefix/foo", Field: "bar"}},
					To: []SecretContext{{
						Cluster:   "foo",
						Namespace: "namspace",
						Name:      "name",
						Type:      corev1.SecretTypeBasicAuth,
					}},
				}},
			},
		},
		{
			name: "DPTP prefix gets added to dockerconfigjson BW items",
			config: Config{
				VaultDPTPPRefix: "prefix",
				Secrets: []SecretConfig{{
					From: map[string]ItemContext{"...": {DockerConfigJSONData: []DockerConfigJSONData{{Item: "foo", AuthField: "bar"}}}},
					To: []SecretContext{{
						Cluster:   "foo",
						Namespace: "namspace",
						Name:      "name",
						Type:      corev1.SecretTypeBasicAuth,
					}},
				}},
			},
			expectedConfig: Config{
				VaultDPTPPRefix: "prefix",
				Secrets: []SecretConfig{{
					From: map[string]ItemContext{"...": {DockerConfigJSONData: []DockerConfigJSONData{{Item: "prefix/foo", AuthField: "bar"}}}},
					To: []SecretContext{{
						Cluster:   "foo",
						Namespace: "namspace",
						Name:      "name",
						Type:      corev1.SecretTypeBasicAuth,
					}},
				}},
			},
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

func TestLoadConfigFromFile(t *testing.T) {
	testCases := []struct {
		name string

		configPath    string
		expected      Config
		expectedError error
	}{
		{
			name:          "file not exist",
			expectedError: fmt.Errorf("open testdata/TestLoadConfigFromFile/file_not_exist.yaml: no such file or directory"),
		},
		{
			name: "basic base",
			expected: Config{
				ClusterGroups: map[string][]string{"build_farm": {"app.ci", "build01", "build02"}},
				Secrets: []SecretConfig{
					{
						From: map[string]ItemContext{
							"ops-mirror.pem": {Item: "mirror.openshift.com", Field: "cert-key.pem"},
							"rh-cdn.pem":     {Item: "rh-cdn", Field: "rh-cdn.pem"},
						},
						To: []SecretContext{{
							Cluster:   "app.ci",
							Namespace: "ocp",
							Name:      "mirror.openshift.com",
						}, {
							Cluster:   "build01",
							Namespace: "ocp",
							Name:      "mirror.openshift.com",
						}, {
							Cluster:   "build02",
							Namespace: "ocp",
							Name:      "mirror.openshift.com",
						}},
					},
				},
			},
		},
		{
			name:          "dup key",
			expectedError: fmt.Errorf("error converting YAML to JSON: yaml: unmarshal errors:\n  line 15: key \"rh-cdn.pem\" already set in map"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actual Config
			actualError := LoadConfigFromFile(filepath.Join("testdata", fmt.Sprintf("%s.yaml", t.Name())), &actual)
			if (tc.expectedError == nil) != (actualError == nil) {
				t.Errorf("%s: expecting error \"%v\", got \"%v\"", t.Name(), tc.expectedError, actualError)
			}
			if tc.expectedError != nil && actualError != nil && tc.expectedError.Error() != actualError.Error() {
				t.Errorf("%s: expecting error msg %q, got %q", t.Name(), tc.expectedError.Error(), actualError.Error())
			}
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("expected config differs from actual config: %s", diff)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name     string
		config   *Config
		expected error
	}{
		{
			name:   "empty case",
			config: &Config{},
		},
		{
			name: "valid",
			config: &Config{Secrets: []SecretConfig{{
				From: map[string]ItemContext{
					".dockerconfigjson": {},
				},
				To: []SecretContext{{
					Cluster: "cl",
					Type:    "kubernetes.io/dockerconfigjson",
				}}}}},
		},
		{
			name: "kubernetes.io/dockerconfigjson type without the desired key",
			config: &Config{Secrets: []SecretConfig{{
				From: map[string]ItemContext{
					"some-key": {},
				},
				To: []SecretContext{{
					Cluster: "cl",
					Type:    "kubernetes.io/dockerconfigjson",
				}}}}},
			expected: utilerrors.NewAggregate([]error{fmt.Errorf("secret[0] in secretConfig[0] with kubernetes.io/dockerconfigjson type have no key named .dockerconfigjson")}),
		},
		{
			name: "long name",
			config: &Config{Secrets: []SecretConfig{{
				From: map[string]ItemContext{
					"some": {},
				},
				To: []SecretContext{{
					Cluster:   "cl",
					Namespace: "test-credentials",
					Name:      "very-very-very-very-very-very-very-very-very-long",
				}}}}},
			expected: utilerrors.NewAggregate([]error{fmt.Errorf("secret[0] in secretConfig[0] cannot be used in a step: volumeName test-credentials-very-very-very-very-very-very-very-very-very-long: [must be no more than 63 characters]")}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.expected, tc.config.Validate(), testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("expected config differs from actual config: %s", diff)
			}
		})
	}
}
