package sanitizer

import (
	"os"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"
)

const blocked = "blocked"

type ClusterMap map[string]string

func LoadClusterConfig(filePath string) (ClusterMap, sets.Set[string], error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}

	var clusters map[string][]string
	err = yaml.Unmarshal(data, &clusters)
	if err != nil {
		return nil, nil, err
	}

	blockedClusters := sets.New[string]()
	clusterMap := make(ClusterMap)
	for provider, clusterList := range clusters {
		if provider != blocked {
			for _, cluster := range clusterList {
				clusterMap[cluster] = provider
			}
		}
		if provider == blocked {
			blockedClusters.Insert(clusterList...)
		}
	}

	return clusterMap, blockedClusters, nil
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
