package dispatcher

import (
	"fmt"
	"os"
	"reflect"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"
)

func loadClusterConfigFromBytes(data []byte) (ClusterMap, sets.Set[string], error) {
	var clusters map[string][]struct {
		Name         string   `yaml:"name"`
		Capacity     int      `yaml:"capacity"`
		Capabilities []string `yaml:"capabilities"`
		Blocked      bool     `yaml:"blocked"`
	}
	if err := yaml.Unmarshal(data, &clusters); err != nil {
		return nil, nil, err
	}
	blockedClusters := sets.New[string]()
	clusterMap := make(ClusterMap)

	providers := make([]string, 0, len(clusters))
	for provider := range clusters {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	seenClusters := make(map[string]string)
	for _, provider := range providers {
		if !knownCloudProviders.Has(provider) {
			return nil, nil, fmt.Errorf("unsupported cloud provider %q", provider)
		}
		clusterList := clusters[provider]
		for _, cluster := range clusterList {
			if cluster.Name == "" {
				return nil, nil, fmt.Errorf("provider %q contains a cluster with an empty name", provider)
			}
			if previousProvider, exists := seenClusters[cluster.Name]; exists {
				return nil, nil, fmt.Errorf("cluster %q is defined more than once under providers %q and %q", cluster.Name, previousProvider, provider)
			}
			seenClusters[cluster.Name] = provider

			if cluster.Capacity == 0 || cluster.Capacity > 100 {
				cluster.Capacity = 100
			} else if cluster.Capacity < 0 {
				cluster.Blocked = true
			}
			if cluster.Blocked {
				blockedClusters.Insert(cluster.Name)
				continue
			}
			clusterMap[cluster.Name] = ClusterInfo{
				Provider:     provider,
				Capacity:     cluster.Capacity,
				Capabilities: sets.List(sets.New[string](cluster.Capabilities...)),
			}
		}
	}

	return clusterMap, blockedClusters, nil
}

// LoadClusterConfig loads cluster configuration from a YAML file, returning a ClusterMap and a set of blocked clusters.
func LoadClusterConfig(filePath string) (ClusterMap, sets.Set[string], error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}
	return loadClusterConfigFromBytes(data)

}

func FindMostUsedCluster(jc *prowconfig.JobConfig) string {
	clusters := make(map[string]int)
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			clusters[job.Cluster]++
		}
	}

	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			clusters[job.Cluster]++
		}
	}
	for _, job := range jc.Periodics {
		clusters[job.Cluster]++
	}
	cluster := ""
	value := 0
	selected := false
	for c, v := range clusters {
		if c == "" {
			continue
		}
		if !selected || v > value || v == value && c < cluster {
			cluster = c
			value = v
			selected = true
		}
	}
	return cluster
}

func DetermineTargetCluster(cluster, determinedCluster, defaultCluster string, canBeRelocated bool, blocked sets.Set[string]) string {
	if cluster == "" {
		cluster = determinedCluster
	}
	var targetCluster string
	if cluster == determinedCluster || canBeRelocated {
		targetCluster = cluster
	} else if _, isBlocked := blocked[determinedCluster]; !isBlocked {
		targetCluster = determinedCluster
	} else {
		targetCluster = cluster
	}

	if _, isBlocked := blocked[targetCluster]; isBlocked {
		return defaultCluster
	}
	return targetCluster
}

func HasCapacityOrCapabilitiesChanged(prev, next ClusterMap) bool {
	for clusterName, info1 := range prev {
		info2, exists := next[clusterName]
		if !exists {
			continue
		}
		if info1.Capacity != info2.Capacity {
			return true
		}
		if info1.Provider != info2.Provider {
			return true
		}
		if !reflect.DeepEqual(info1.Capabilities, info2.Capabilities) {
			return true
		}
	}

	return false
}
