package api

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// sortBundlesByCluster sorts bundles by their first target's cluster name for deterministic comparison.
// Uses multiple sort keys to ensure stable ordering: cluster, bundle name, then namespace.
func sortBundlesByCluster(bundles []GSMBundle) {
	sort.Slice(bundles, func(i, j int) bool {
		clusterI := ""
		if len(bundles[i].Targets) > 0 {
			clusterI = bundles[i].Targets[0].Cluster
		}
		clusterJ := ""
		if len(bundles[j].Targets) > 0 {
			clusterJ = bundles[j].Targets[0].Cluster
		}

		if clusterI != clusterJ {
			return clusterI < clusterJ
		}

		if bundles[i].Name != bundles[j].Name {
			return bundles[i].Name < bundles[j].Name
		}

		if len(bundles[i].Targets) > 0 && len(bundles[j].Targets) > 0 {
			return bundles[i].Targets[0].Namespace < bundles[j].Targets[0].Namespace
		}

		return false
	})
}

func TestGSMConfigResolve(t *testing.T) {
	testCases := []struct {
		name           string
		config         GSMConfig
		expectedError  string
		expectedConfig GSMConfig
	}{
		{
			name: "cluster_groups expansion - single group",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"build-clusters"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
							{Cluster: "build02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "cluster_groups expansion - multiple groups",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
					"app-clusters":   {"app01", "app02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"build-clusters", "app-clusters"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
					"app-clusters":   {"app01", "app02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
							{Cluster: "app01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
							{Cluster: "app02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "cluster_groups expansion - preserves explicit Type",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"build-clusters"}, Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
		},
		{
			name: "direct cluster - not expanded",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "specific-cluster", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "specific-cluster", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "component resolution - single component",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "component-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: []string{"my-component"},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "component-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: nil,
						GSMSecrets: []GSMSecretRef{
							{Collection: "component-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "component resolution - multiple components",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"component-a": {
						{Collection: "secrets-a", Group: "group1"},
					},
					"component-b": {
						{Collection: "secrets-b", Group: "group2"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: []string{"component-a", "component-b"},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"component-a": {
						{Collection: "secrets-a", Group: "group1"},
					},
					"component-b": {
						{Collection: "secrets-b", Group: "group2"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: nil,
						GSMSecrets: []GSMSecretRef{
							{Collection: "secrets-a", Group: "group1"},
							{Collection: "secrets-b", Group: "group2"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "component resolution - merges with existing gsm_secrets",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "component-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: []string{"my-component"},
						GSMSecrets: []GSMSecretRef{
							{Collection: "bundle-secrets", Group: "group2"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "component-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: nil,
						GSMSecrets: []GSMSecretRef{
							{Collection: "bundle-secrets", Group: "group2"},
							{Collection: "component-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "${CLUSTER} substitution - single cluster",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-${CLUSTER}"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-build01"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "${CLUSTER} substitution - multiple clusters creates separate bundles",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-${CLUSTER}"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
							{Cluster: "build02", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-build01"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-build02"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "${CLUSTER} substitution - preserves 'as' field",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-${CLUSTER}", As: "config"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "kubeconfig-build01", As: "config"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "all phases combined - cluster_groups + components + ${CLUSTER}",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Components: map[string][]GSMSecretRef{
					"base-component": {
						{Collection: "base-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: []string{"base-component"},
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group2",
								Fields: []FieldEntry{
									{Name: "token-${CLUSTER}"},
								},
							},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"build-clusters"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Components: map[string][]GSMSecretRef{
					"base-component": {
						{Collection: "base-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: nil,
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group2",
								Fields: []FieldEntry{
									{Name: "token-build01"},
								},
							},
							{Collection: "base-secrets", Group: "group1", Fields: []FieldEntry{}},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
					{
						Name:       "test-bundle",
						Components: nil,
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group2",
								Fields: []FieldEntry{
									{Name: "token-build02"},
								},
							},
							{Collection: "base-secrets", Group: "group1", Fields: []FieldEntry{}},
						},
						Targets: []TargetSpec{
							{Cluster: "build02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "error: both cluster and cluster_groups set",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "direct-cluster", ClusterGroups: []string{"build-clusters"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedError: "bundle 0 target 0 has both cluster and cluster_groups set, those are mutually exclusive",
		},
		{
			name: "error: non-existent cluster_group",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"non-existent-group"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedError: "bundle 0 target 0 references non-existent cluster_group non-existent-group",
		},
		{
			name: "error: non-existent component",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"existing-component": {
						{Collection: "secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name:       "test-bundle",
						Components: []string{"non-existent-component"},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectedError: "bundle test-bundle references non-existent component: non-existent-component",
		},
		{
			name: "SyncToCluster field preserved through resolve",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{ClusterGroups: []string{"build-clusters"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01", "build02"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
							{Cluster: "build02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "multiple ${CLUSTER} variables in same GSMSecretRef",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "token-${CLUSTER}"},
									{Name: "kubeconfig-${CLUSTER}"},
									{Name: "cert-${CLUSTER}", As: "certificate"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
							{Cluster: "build02", Namespace: "ci"},
						},
					},
				},
			},
			expectedConfig: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "token-build01"},
									{Name: "kubeconfig-build01"},
									{Name: "cert-build01", As: "certificate"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "cluster-specific",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "token-build02"},
									{Name: "kubeconfig-build02"},
									{Name: "cert-build02", As: "certificate"},
								},
							},
						},
						Targets: []TargetSpec{
							{Cluster: "build02", Namespace: "ci", Type: corev1.SecretTypeOpaque},
						},
					},
				},
			},
		},
		{
			name: "error: ${CLUSTER} variable with no resolvable targets",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle-with-cluster-var",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "test-secrets",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "token-${CLUSTER}"},
								},
							},
						},
						Targets: []TargetSpec{
							{ClusterGroups: []string{"non-existent-group"}, Namespace: "ci"},
						},
					},
				},
			},
			expectedError: `[bundle 0 target 0 references non-existent cluster_group non-existent-group, bundle "test-bundle-with-cluster-var" uses ${CLUSTER} variable substitution but has no resolvable targets (check that cluster_groups or cluster references are valid)]`,
		},
		{
			name: "error: ${CLUSTER} variable with empty targets",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle-no-targets",
						GSMSecrets: []GSMSecretRef{
							{
								Collection: "test-secrets",
								Group:      "group1",
								Fields: []FieldEntry{
									{Name: "token-${CLUSTER}"},
								},
							},
						},
						Targets: []TargetSpec{},
					},
				},
			},
			expectedError: `bundle "test-bundle-no-targets" uses ${CLUSTER} variable substitution but has no resolvable targets (check that cluster_groups or cluster references are valid)`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.resolve()

			if tc.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.expectedError)
				}
				if err.Error() != tc.expectedError {
					t.Fatalf("expected error %q, got %q", tc.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Sort bundles for deterministic comparison (map iteration order is non-deterministic)
			sortBundlesByCluster(tc.config.Bundles)
			sortBundlesByCluster(tc.expectedConfig.Bundles)

			if diff := cmp.Diff(tc.expectedConfig, tc.config); diff != "" {
				t.Errorf("config differs from expected:\n%s", diff)
			}
		})
	}
}

func TestGSMConfigResolveFromYAML(t *testing.T) {
	fixturesDir := filepath.Join("testdata", "gsm-resolve")

	testCases := []struct {
		name          string
		inputFile     string
		expectedFile  string
		expectedError string
	}{
		{
			name:         "complex hierarchy example",
			inputFile:    "complex-input.yaml",
			expectedFile: "complex-expected.yaml",
		},
		{
			name:         "cluster variable with dockerconfig",
			inputFile:    "dockerconfig-cluster-var-input.yaml",
			expectedFile: "dockerconfig-cluster-var-expected.yaml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inputPath := filepath.Join(fixturesDir, tc.inputFile)
			expectedPath := filepath.Join(fixturesDir, tc.expectedFile)

			var config GSMConfig
			if err := LoadGSMConfigFromFile(inputPath, &config); err != nil {
				t.Fatalf("failed to load input config: %v", err)
			}
			err := config.resolve()

			if tc.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.expectedError)
				}
				if err.Error() != tc.expectedError {
					t.Fatalf("expected error %q, got %q", tc.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var expectedConfig GSMConfig
			if err := LoadGSMConfigFromFile(expectedPath, &expectedConfig); err != nil {
				t.Fatalf("failed to load expected config: %v", err)
			}

			// Sort bundles for deterministic comparison (map iteration order is non-deterministic)
			sortBundlesByCluster(config.Bundles)
			sortBundlesByCluster(expectedConfig.Bundles)

			if diff := cmp.Diff(expectedConfig, config); diff != "" {
				t.Errorf("config differs from expected:\n%s", diff)
			}
		})
	}
}

func TestGSMConfigUnmarshalJSON(t *testing.T) {
	t.Run("resolve is called during unmarshal", func(t *testing.T) {
		yamlData := `
cluster_groups:
  build-clusters: [build01, build02]
bundles:
  - name: test-bundle
    gsm_secrets:
      - collection: test-secrets
        group: group1
    targets:
      - cluster_groups: [build-clusters]
        namespace: ci
`
		var config GSMConfig
		if err := yaml.UnmarshalStrict([]byte(yamlData), &config); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// After unmarshal, cluster_groups should be expanded
		if len(config.Bundles) != 1 {
			t.Fatalf("expected 1 bundle, got %d", len(config.Bundles))
		}

		bundle := config.Bundles[0]
		if len(bundle.Targets) != 2 {
			t.Fatalf("expected 2 targets after cluster_groups expansion, got %d", len(bundle.Targets))
		}

		expectedClusters := []string{"build01", "build02"}
		actualClusters := []string{bundle.Targets[0].Cluster, bundle.Targets[1].Cluster}

		if diff := cmp.Diff(expectedClusters, actualClusters); diff != "" {
			t.Errorf("cluster names differ from expected:\n%s", diff)
		}
	})
}

func TestLoadGSMConfigFromFile(t *testing.T) {
	t.Run("loads and resolves config from YAML file", func(t *testing.T) {
		// Create a temporary YAML file
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "test-config.yaml")

		yamlContent := `
cluster_groups:
  test-group: [cluster1]
bundles:
  - name: test-bundle
    gsm_secrets:
      - collection: secrets
        group: group1
    targets:
      - cluster_groups: [test-group]
        namespace: default
`
		if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		var config GSMConfig
		if err := LoadGSMConfigFromFile(configFile, &config); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify cluster_groups were expanded
		if len(config.Bundles[0].Targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(config.Bundles[0].Targets))
		}

		if config.Bundles[0].Targets[0].Cluster != "cluster1" {
			t.Errorf("expected cluster 'cluster1', got %q", config.Bundles[0].Targets[0].Cluster)
		}
	})
}

func TestGSMConfigValidate(t *testing.T) {
	testCases := []struct {
		name          string
		config        GSMConfig
		expectError   bool
		errorContains string
	}{
		{
			name: "valid config with all fields",
			config: GSMConfig{
				ClusterGroups: map[string][]string{
					"build-clusters": {"build01"},
				},
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "test-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1", Fields: []FieldEntry{{Name: "token"}}},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid config - auto discovery (no fields)",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "error: component with empty name",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"": {
						{Collection: "test-secrets", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component has empty name",
		},
		{
			name: "error: component with no GSM secret references",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component has no GSM secret references",
		},
		{
			name: "error: component GSMSecretRef with empty collection",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component[0] has empty collection",
		},
		{
			name: "error: component GSMSecretRef with invalid collection",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "some-malformed$(-collection name", Group: "group1"},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component[0] has invalid collection string",
		},
		{
			name: "error: component GSMSecretRef with invalid group",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "valid-collection", Group: "invalid$group!name"},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component[0] has invalid group string",
		},
		{
			name: "error: component GSMSecretRef with invalid field name",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "valid-collection", Group: "group1", Fields: []FieldEntry{{Name: "bad$field!name"}}},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component[0].secrets[0] has invalid name",
		},
		{
			name: "error: component with neither fields nor group",
			config: GSMConfig{
				Components: map[string][]GSMSecretRef{
					"my-component": {
						{Collection: "test-secrets", Group: "", Fields: []FieldEntry{}},
					},
				},
				Bundles: []GSMBundle{},
			},
			expectError:   true,
			errorContains: "component my-component[0] has neither group nor any fields defined",
		},
		{
			name: "error: bundle with empty name",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle[0] has empty name",
		},
		{
			name: "error: duplicate bundle by name+cluster+namespace",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "secrets1", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "secrets2", Group: "group2"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "duplicate bundle: name=test-bundle cluster=build01 namespace=ci",
		},
		{
			name: "error: sync_to_cluster true but no targets",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets:       []TargetSpec{},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle has sync_to_cluster: true but no targets",
		},
		{
			name: "error: sync_to_cluster false but has targets",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: false,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle has sync_to_cluster: false but has targets",
		},
		{
			name: "error: target with empty namespace",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: ""},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle target[0] has empty namespace",
		},
		{
			name: "error: target with empty cluster",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle target[0] has empty cluster",
		},
		{
			name: "error: target with invalid type",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: "kubernetes.io/tls"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle target[0] has invalid type (kubernetes.io/tls)",
		},
		{
			name: "error: GSMSecretRef with empty collection",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "", Group: "group1"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle gsm_secrets[0] has empty collection",
		},
		{
			name: "error: GSMSecretRef with no secrets and no group",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Fields: []FieldEntry{}},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle gsm_secrets[0] has no secrets and no group defined",
		},
		{
			name: "error: bundle GSMSecretRef with invalid group",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "bad$group!name"},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle gsm_secrets[0] has invalid group string",
		},
		{
			name: "error: field with empty name",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1", Fields: []FieldEntry{{Name: ""}}},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle gsm_secrets[0].secrets[0] has empty name",
		},
		{
			name: "error: bundle GSMSecretRef with invalid field name",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						GSMSecrets: []GSMSecretRef{
							{Collection: "test-secrets", Group: "group1", Fields: []FieldEntry{{Name: "invalid$field!name"}}},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle gsm_secrets[0].secrets[0] has invalid name",
		},
		{
			name: "valid config - dockerconfig with empty 'as' field defaults to .dockerconfigjson",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						DockerConfig: &DockerConfigSpec{
							As: "",
							Registries: []RegistryAuthData{
								{Collection: "creds", Group: "group1", RegistryURL: "quay.io", AuthField: "auth"},
							},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "error: dockerconfig with no registries",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						DockerConfig: &DockerConfigSpec{
							As:         ".dockerconfigjson",
							Registries: []RegistryAuthData{},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "dockerconfig has no registries",
		},
		{
			name: "error: dockerconfig registry with empty collection",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						DockerConfig: &DockerConfigSpec{
							Registries: []RegistryAuthData{
								{Collection: "", Group: "group1", RegistryURL: "quay.io", AuthField: "auth"},
							},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle[0] test-bundle dockerconfig registry[0] has invalid collection string",
		},
		{
			name: "error: dockerconfig registry with empty registry_url",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						DockerConfig: &DockerConfigSpec{
							As: ".dockerconfigjson",
							Registries: []RegistryAuthData{
								{Collection: "creds", Group: "group1", RegistryURL: "", AuthField: "auth"},
							},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "dockerconfig registry[0] has empty registry_url",
		},
		{
			name: "error: dockerconfig registry with empty auth_field",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name: "test-bundle",
						DockerConfig: &DockerConfigSpec{
							As: ".dockerconfigjson",
							Registries: []RegistryAuthData{
								{Collection: "creds", Group: "group1", RegistryURL: "quay.io", AuthField: ""},
							},
						},
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci", Type: corev1.SecretTypeDockerConfigJson},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "dockerconfig registry[0] has empty auth_field",
		},
		{
			name: "error: bundle with neither gsm_secrets, dockerconfig, nor components",
			config: GSMConfig{
				Bundles: []GSMBundle{
					{
						Name:          "test-bundle",
						SyncToCluster: true,
						Targets: []TargetSpec{
							{Cluster: "build01", Namespace: "ci"},
						},
					},
				},
			},
			expectError:   true,
			errorContains: "bundle test-bundle has neither gsm_secrets, dockerconfig, nor components",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errorContains)
				}
				if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Fatalf("expected error containing %q, got %q", tc.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
