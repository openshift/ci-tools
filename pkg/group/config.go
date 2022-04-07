package group

import (
	"fmt"
	"io/ioutil"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

const (
	// OpenshiftPrivAdminsGroup defines the group that will be used for the openshift-priv namespace in the app.ci cluster.
	OpenshiftPrivAdminsGroup = "openshift-priv-admins"
)

// Config represents the configuration file for the groups
type Config struct {
	// ClusterGroups holds the mapping from cluster group name to the clusters in the group
	ClusterGroups map[string][]string `json:"cluster_groups,omitempty"`
	// Groups holds the mapping from group name to its target
	Groups map[string]Target `json:"groups,omitempty"`
}

// Target represents the distribution of a group
// If neither Clusters and nor ClusterGroups is set, then the group is on all clusters.
type Target struct {
	// RenameTo is the new name of the group. If not set, the original name will be used.
	RenameTo string `json:"rename_to,omitempty"`
	// Clusters is the clusters where the group should exist.
	Clusters []string `json:"clusters,omitempty"`
	// ClusterGroups is the cluster groups where the group should exist.
	ClusterGroups []string `json:"cluster_groups,omitempty"`
}

func (t Target) ResolveClusters(cg map[string][]string) sets.String {
	ret := sets.NewString(t.Clusters...)
	for _, clusterGroup := range t.ClusterGroups {
		ret.Insert(cg[clusterGroup]...)
	}
	return ret
}

// LoadConfig loads the config from a given file
func LoadConfig(file string) (*Config, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to load config file")
	}
	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file: %w", err)
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config file: %w", err)
	}
	return config, nil
}

func (c *Config) validate() error {
	for k, v := range c.Groups {
		if k == OpenshiftPrivAdminsGroup || v.RenameTo == OpenshiftPrivAdminsGroup {
			return fmt.Errorf("cannot use the group name %s in the configuration file", OpenshiftPrivAdminsGroup)
		}
	}
	return nil
}
