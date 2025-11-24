package secretbootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

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
					From: nil,
					To: []SecretContext{
						{
							ClusterGroups: []string{"group-a", "group-b"},
							Cluster:       "a",
							Namespace:     "namspace",
							Name:          "name",
							Type:          corev1.SecretTypeBasicAuth,
						},
						{
							ClusterGroups: []string{"group-a", "group-b"},
							Cluster:       "b",
							Namespace:     "namspace",
							Name:          "name",
							Type:          corev1.SecretTypeBasicAuth,
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
				VaultDPTPPrefix: "prefix",
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
				VaultDPTPPrefix: "prefix",
				ClusterGroups:   nil,
				Secrets: []SecretConfig{{
					From: map[string]ItemContext{"...": {
						Item:                 "prefix/foo",
						Field:                "bar",
						DockerConfigJSONData: nil,
					}},
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
				VaultDPTPPrefix: "prefix",
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
				VaultDPTPPrefix: "prefix",
				ClusterGroups:   nil,
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
		name          string
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
							"ops-mirror.pem": {
								Item:                 "dptp/mirror.openshift.com",
								Field:                "cert-key.pem",
								DockerConfigJSONData: nil,
							},
							"rh-cdn.pem": {
								Item:                 "dptp/rh-cdn",
								Field:                "rh-cdn.pem",
								DockerConfigJSONData: nil,
							},
						},
						To: []SecretContext{{
							ClusterGroups: []string{"build_farm"},
							Cluster:       "app.ci",
							Namespace:     "ocp",
							Name:          "mirror.openshift.com",
						}, {
							ClusterGroups: []string{"build_farm"},
							Cluster:       "build01",
							Namespace:     "ocp",
							Name:          "mirror.openshift.com",
						}, {
							ClusterGroups: []string{"build_farm"},
							Cluster:       "build02",
							Namespace:     "ocp",
							Name:          "mirror.openshift.com",
						}},
					},
				},
				VaultDPTPPrefix:           "dptp",
				UserSecretsTargetClusters: []string{"app.ci", "build01", "build02"},
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

// normalizeConfigForComparison sorts the Config data structures for deterministic comparison in tests
// This is a test-only function that ignores order when comparing configs
func normalizeConfigForComparison(c *Config) {
	// Sort ClusterGroups map keys
	sortedClusterGroups := make(map[string][]string)
	clusterGroupKeys := make([]string, 0, len(c.ClusterGroups))
	for k := range c.ClusterGroups {
		clusterGroupKeys = append(clusterGroupKeys, k)
	}
	sort.Strings(clusterGroupKeys)
	for _, k := range clusterGroupKeys {
		clusters := make([]string, len(c.ClusterGroups[k]))
		copy(clusters, c.ClusterGroups[k])
		sort.Strings(clusters)
		sortedClusterGroups[k] = clusters
	}
	c.ClusterGroups = sortedClusterGroups

	// Sort Secrets slice
	sort.Slice(c.Secrets, func(i, j int) bool {
		// Sort by first To entry's Cluster, then Namespace, then Name
		// This groups secrets by cluster, making the output more organized
		if len(c.Secrets[i].To) == 0 && len(c.Secrets[j].To) == 0 {
			return false
		}
		if len(c.Secrets[i].To) == 0 {
			return true
		}
		if len(c.Secrets[j].To) == 0 {
			return false
		}
		toI := c.Secrets[i].To[0]
		toJ := c.Secrets[j].To[0]
		if toI.Cluster != toJ.Cluster {
			return toI.Cluster < toJ.Cluster
		}
		if toI.Namespace != toJ.Namespace {
			return toI.Namespace < toJ.Namespace
		}
		return toI.Name < toJ.Name
	})

	// Sort UserSecretsTargetClusters
	sort.Strings(c.UserSecretsTargetClusters)

	// Order each SecretConfig's DockerConfigJSONData slices for deterministic ordering
	// (YAML marshaler already sorts map keys, so we only need to sort slices)
	for i := range c.Secrets {
		for k, itemCtx := range c.Secrets[i].From {
			if len(itemCtx.DockerConfigJSONData) > 0 {
				sortedData := make([]DockerConfigJSONData, len(itemCtx.DockerConfigJSONData))
				copy(sortedData, itemCtx.DockerConfigJSONData)
				sort.Slice(sortedData, func(i, j int) bool {
					if sortedData[i].RegistryURL != sortedData[j].RegistryURL {
						return sortedData[i].RegistryURL < sortedData[j].RegistryURL
					}
					if sortedData[i].Item != sortedData[j].Item {
						return sortedData[i].Item < sortedData[j].Item
					}
					return sortedData[i].AuthField < sortedData[j].AuthField
				})
				itemCtx.DockerConfigJSONData = sortedData
				c.Secrets[i].From[k] = itemCtx
			}
		}
	}
}

func TestRoundtripConfig(t *testing.T) {
	testCases := []struct {
		name string
	}{
		{
			name: "basic base",
		},
	}

	for _, tc := range testCases {
		var outFile string
		t.Run(tc.name, func(t *testing.T) {
			bytes := testhelper.ReadFromFixture(t, "")
			c := &Config{}
			err := yaml.Unmarshal(bytes, c)
			if err != nil {
				t.Fatalf("error unmarshaling config file: %v", err)
			}

			outFile = filepath.Join("testdata", "roundtrip_out.yaml")
			err = SaveConfigToFile(outFile, c)
			if err != nil {
				t.Fatalf("error saving config file: %v", err)
			}

			// Load both input and output files into Config structs and normalize them
			// This allows comparison regardless of the input file's ordering
			inFile := filepath.Join("testdata", "zz_fixture_TestRoundtripConfig_basic_base.yaml")
			var inputConfig, outputConfig Config
			if err := LoadConfigFromFile(inFile, &inputConfig); err != nil {
				t.Fatalf("error loading input config file: %v", err)
			}
			if err := LoadConfigFromFile(outFile, &outputConfig); err != nil {
				t.Fatalf("error loading output config file: %v", err)
			}

			// Normalize both configs for comparison (test-only, ignores order)
			normalizeConfigForComparison(&inputConfig)
			normalizeConfigForComparison(&outputConfig)

			if diff := cmp.Diff(inputConfig, outputConfig); diff != "" {
				t.Fatalf("input and output configs are not equal. %s", diff)
			}
		})

		t.Cleanup(func() {
			if err := os.Remove(outFile); err != nil {
				t.Fatalf("error removing output config file: %v", err)
			}
		})
	}
}

func TestGroupClusters(t *testing.T) {
	testCases := []struct {
		name     string
		input    SecretConfig
		expected []SecretContext
	}{
		{
			name: "no group",
			input: SecretConfig{
				From: map[string]ItemContext{
					"item-a": {
						Item:  "a",
						Field: "field",
					},
				},
				To: []SecretContext{{
					Cluster:   "cluster1",
					Namespace: "ns",
					Name:      "a",
				}},
			},
			expected: []SecretContext{{
				Cluster:   "cluster1",
				Namespace: "ns",
				Name:      "a",
			}},
		},
		{
			name: "group",
			input: SecretConfig{
				From: map[string]ItemContext{
					"item-a": {
						Item:  "a",
						Field: "field",
					},
				},
				To: []SecretContext{{
					ClusterGroups: []string{"group-a"},
					Cluster:       "cluster1",
					Namespace:     "ns",
					Name:          "a",
				}},
			},
			expected: []SecretContext{{
				ClusterGroups: []string{"group-a"},
				Namespace:     "ns",
				Name:          "a",
			}},
		},
		{
			name: "mix",
			input: SecretConfig{
				From: map[string]ItemContext{
					"item-a": {
						Item:  "a",
						Field: "field",
					},
				},
				To: []SecretContext{
					{
						ClusterGroups: []string{"group-a"},
						Cluster:       "cluster1",
						Namespace:     "ns",
						Name:          "a",
					},
					{
						Cluster:   "cluster2",
						Namespace: "ns",
						Name:      "b",
					},
				},
			},
			expected: []SecretContext{
				{
					ClusterGroups: []string{"group-a"},
					Namespace:     "ns",
					Name:          "a",
				},
				{
					Cluster:   "cluster2",
					Namespace: "ns",
					Name:      "b",
				},
			},
		},
		{
			name: "multiple groups",
			input: SecretConfig{
				From: map[string]ItemContext{
					"item-a": {
						Item:  "a",
						Field: "field",
					},
				},
				To: []SecretContext{{
					ClusterGroups: []string{"group-a", "group-b"},
					Cluster:       "cluster1",
					Namespace:     "ns",
					Name:          "a",
				}},
			},
			expected: []SecretContext{{
				ClusterGroups: []string{"group-a", "group-b"},
				Namespace:     "ns",
				Name:          "a",
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sc := tc.input
			sc.groupClusters()
			if diff := cmp.Diff(tc.expected, sc.To); diff != "" {
				t.Fatalf("result of groupClusters() doesn't match expected output: %v", diff)
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
