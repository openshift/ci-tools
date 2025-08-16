package dispatcher

import (
	"fmt"
	"os"
	"reflect"
	"strings"

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

func HasCapacityOrCapabilitiesChanged(prev, next ClusterMap) bool {
	for clusterName, info1 := range prev {
		info2, exists := next[clusterName]
		if !exists {
			continue
		}
		if info1.Capacity != info2.Capacity {
			return true
		}
		if !reflect.DeepEqual(info1.Capabilities, info2.Capabilities) {
			return true
		}
	}

	return false
}

// SaveClusterConfigPreservingFormat saves the ClusterMap to YAML while preserving the original format, order, and case sensitivity
// This method reconstructs the YAML with the exact same structure and field order as the original
func SaveClusterConfigPreservingFormat(clusterMap ClusterMap, blockedClusters sets.Set[string], originalFilePath, outputFilePath string) error {
	// Read and parse the original file to understand the structure and order
	originalData, err := os.ReadFile(originalFilePath)
	if err != nil {
		return err
	}

	// Parse the original YAML structure
	var originalClusters map[string][]struct {
		Name         string   `yaml:"name"`
		Capacity     int      `yaml:"capacity"`
		Capabilities []string `yaml:"capabilities"`
		Blocked      bool     `yaml:"blocked"`
	}
	if err := yaml.Unmarshal(originalData, &originalClusters); err != nil {
		return err
	}

	// Analyze the original file to understand field order and explicit blocked entries
	originalLines := strings.Split(string(originalData), "\n")
	explicitBlockedFalse := make(map[string]bool)
	clusterFieldOrder := make(map[string][]string) // cluster name -> field order

	// Parse the original to understand field order for each cluster
	currentCluster := ""
	for _, line := range originalLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- name: ") {
			currentCluster = strings.TrimPrefix(trimmed, "- name: ")
			clusterFieldOrder[currentCluster] = []string{"name"} // name is always first
		} else if currentCluster != "" && strings.HasPrefix(line, "    ") {
			// This is a field for the current cluster
			if strings.Contains(line, "blocked: false") {
				explicitBlockedFalse[currentCluster] = true
				clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "blocked")
			} else if strings.Contains(line, "blocked: true") {
				clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "blocked")
			} else if strings.Contains(line, "capacity:") {
				clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "capacity")
			} else if strings.Contains(line, "capabilities:") {
				clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "capabilities")
			}
		}
	}

	var output strings.Builder
	processedClusters := make(map[string]bool)

	// Reconstruct the YAML maintaining the original provider order
	providers := make([]string, 0, len(originalClusters))
	for provider := range originalClusters {
		providers = append(providers, provider)
	}
	// Keep the original order by parsing line by line to detect provider order
	providerOrder := []string{}
	for _, line := range originalLines {
		if len(line) > 0 && line[0] != ' ' && line[0] != '-' && strings.HasSuffix(line, ":") {
			provider := strings.TrimSuffix(line, ":")
			// Add to order if not already present
			found := false
			for _, p := range providerOrder {
				if p == provider {
					found = true
					break
				}
			}
			if !found {
				providerOrder = append(providerOrder, provider)
			}
		}
	}

	for _, provider := range providerOrder {
		output.WriteString(fmt.Sprintf("%s:\n", provider))

		clusters := originalClusters[provider]
		for _, originalCluster := range clusters {
			clusterName := originalCluster.Name
			processedClusters[clusterName] = true

			output.WriteString(fmt.Sprintf("  - name: %s\n", clusterName))

			// Check if this cluster exists in our ClusterMap
			if clusterInfo, exists := clusterMap[clusterName]; exists {
				// Write fields in the original order
				fieldOrder := clusterFieldOrder[clusterName]
				if len(fieldOrder) == 0 {
					// Default order if not found
					fieldOrder = []string{"name", "capacity", "capabilities", "blocked"}
				}

				for _, field := range fieldOrder {
					switch field {
					case "capacity":
						if originalCluster.Capacity > 0 || (clusterInfo.Capacity != 100 && clusterInfo.Capacity != 0) {
							capacity := clusterInfo.Capacity
							if capacity == 0 || capacity == 100 {
								if originalCluster.Capacity > 0 {
									capacity = originalCluster.Capacity
								}
							}
							if capacity > 0 && capacity != 100 {
								output.WriteString(fmt.Sprintf("    capacity: %d\n", capacity))
							}
						}
					case "capabilities":
						if len(clusterInfo.Capabilities) > 0 {
							output.WriteString("    capabilities:\n")
							for _, cap := range clusterInfo.Capabilities {
								output.WriteString(fmt.Sprintf("    - %s\n", cap))
							}
						}
					case "blocked":
						if blockedClusters.Has(clusterName) {
							output.WriteString("    blocked: true\n")
						} else if explicitBlockedFalse[clusterName] {
							output.WriteString("    blocked: false\n")
						}
					}
				}
			} else {
				// Cluster not in ClusterMap, preserve original structure but mark as blocked
				fieldOrder := clusterFieldOrder[clusterName]
				if len(fieldOrder) == 0 {
					fieldOrder = []string{"name", "capacity", "capabilities", "blocked"}
				}

				for _, field := range fieldOrder {
					switch field {
					case "capacity":
						if originalCluster.Capacity > 0 {
							output.WriteString(fmt.Sprintf("    capacity: %d\n", originalCluster.Capacity))
						}
					case "capabilities":
						if len(originalCluster.Capabilities) > 0 {
							output.WriteString("    capabilities:\n")
							for _, cap := range originalCluster.Capabilities {
								output.WriteString(fmt.Sprintf("    - %s\n", cap))
							}
						}
					case "blocked":
						output.WriteString("    blocked: true\n")
					}
				}
			}
		}
	}

	// Add any new clusters that weren't in the original file
	newClusters := make(map[string][]string) // provider -> cluster names
	for clusterName, clusterInfo := range clusterMap {
		if !processedClusters[clusterName] {
			newClusters[clusterInfo.Provider] = append(newClusters[clusterInfo.Provider], clusterName)
		}
	}

	// Add new clusters to existing providers or create new provider sections
	for provider, clusterNames := range newClusters {
		// Check if provider already exists in output
		if !strings.Contains(output.String(), provider+":") {
			// Add new provider section
			output.WriteString(fmt.Sprintf("\n%s:\n", provider))
		}

		for _, clusterName := range clusterNames {
			clusterInfo := clusterMap[clusterName]
			output.WriteString(fmt.Sprintf("  - name: %s\n", clusterName))

			if clusterInfo.Capacity != 100 && clusterInfo.Capacity != 0 {
				output.WriteString(fmt.Sprintf("    capacity: %d\n", clusterInfo.Capacity))
			}

			if len(clusterInfo.Capabilities) > 0 {
				output.WriteString("    capabilities:\n")
				for _, cap := range clusterInfo.Capabilities {
					output.WriteString(fmt.Sprintf("    - %s\n", cap))
				}
			}

			if blockedClusters.Has(clusterName) {
				output.WriteString("    blocked: true\n")
			}
		}
	}

	// Write the output to file
	return os.WriteFile(outputFilePath, []byte(output.String()), 0644)
}
