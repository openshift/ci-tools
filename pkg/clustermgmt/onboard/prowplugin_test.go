package onboard

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/plugins"
)

func TestUpdateProwPluginConfigConfigUpdater(t *testing.T) {
	testCases := []struct {
		name        string
		clusterName string
		input       plugins.Configuration
		expected    plugins.Configuration
	}{
		{
			name:        "empty config",
			clusterName: "new-cluster",
			input:       plugins.Configuration{},
			expected: plugins.Configuration{
				ConfigUpdater: plugins.ConfigUpdater{
					ClusterGroups: map[string]plugins.ClusterGroup{
						"build_farm_ci":  {Clusters: []string{"new-cluster"}, Namespaces: []string{"ci"}},
						"build_farm_ocp": {Clusters: []string{"new-cluster"}, Namespaces: []string{"ocp"}},
					},
				},
			},
		},
		{
			name:        "some config",
			clusterName: "new-cluster",
			input: plugins.Configuration{
				ConfigUpdater: plugins.ConfigUpdater{
					ClusterGroups: map[string]plugins.ClusterGroup{
						"build_farm_ci":  {Clusters: []string{"existing-cluster"}, Namespaces: []string{"ci"}},
						"build_farm_ocp": {Clusters: []string{"existing-cluster"}, Namespaces: []string{"ocp"}},
					},
				},
			},
			expected: plugins.Configuration{
				ConfigUpdater: plugins.ConfigUpdater{
					ClusterGroups: map[string]plugins.ClusterGroup{
						"build_farm_ci":  {Clusters: []string{"existing-cluster", "new-cluster"}, Namespaces: []string{"ci"}},
						"build_farm_ocp": {Clusters: []string{"existing-cluster", "new-cluster"}, Namespaces: []string{"ocp"}},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewProwPluginStep(logrus.NewEntry(logrus.StandardLogger()), &clusterinstall.ClusterInstall{})
			s.updateProwPluginConfigConfigUpdater(&tc.input, tc.clusterName)
			if diff := cmp.Diff(tc.expected, tc.input); diff != "" {
				t.Fatalf("expected jobs were different than results: %s", diff)
			}
		})
	}
}
