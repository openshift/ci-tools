package sanitizer

import (
	"os"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"
)

// ClusterInfo holds the provider, capacity, and capabilities.
type ClusterInfo struct {
	Provider     string
	Capacity     int
	Capabilities []string
}

// ClusterMap maps a cluster name to its corresponding ClusterInfo.
type ClusterMap map[string]ClusterInfo

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

	for provider, clusterList := range clusters {
		for _, cluster := range clusterList {
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
				Capabilities: cluster.Capabilities,
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
	for c, v := range clusters {
		if v > value {
			cluster = c
			value = v
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
