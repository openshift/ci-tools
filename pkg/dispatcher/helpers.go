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

type originalClusterInfo struct {
	Name         string   `yaml:"name"`
	Capacity     int      `yaml:"capacity"`
	Capabilities []string `yaml:"capabilities"`
	Blocked      bool     `yaml:"blocked"`
}

type formatPreservationData struct {
	originalClusters     map[string][]originalClusterInfo
	explicitBlockedFalse map[string]bool
	clusterFieldOrder    map[string][]string
	providerOrder        []string
}

// SaveClusterConfigPreservingFormat saves the ClusterMap to YAML while preserving the original format, order, and case sensitivity
func SaveClusterConfigPreservingFormat(clusterMap ClusterMap, blockedClusters sets.Set[string], originalFilePath, outputFilePath string) error {
	originalData, err := os.ReadFile(originalFilePath)
	if err != nil {
		return err
	}

	formatData, err := parseOriginalFormat(originalData)
	if err != nil {
		return err
	}

	var output strings.Builder
	processedClusters := writeExistingClusters(&output, formatData, clusterMap, blockedClusters)
	writeNewClusters(&output, clusterMap, blockedClusters, processedClusters)

	return os.WriteFile(outputFilePath, []byte(output.String()), 0644)
}

// parseOriginalFormat analyzes the original YAML to preserve structure and field order
func parseOriginalFormat(originalData []byte) (*formatPreservationData, error) {
	var originalClusters map[string][]originalClusterInfo
	if err := yaml.Unmarshal(originalData, &originalClusters); err != nil {
		return nil, err
	}

	originalLines := strings.Split(string(originalData), "\n")
	explicitBlockedFalse := make(map[string]bool)
	clusterFieldOrder := make(map[string][]string)
	providerOrder := extractProviderOrder(originalLines)

	parseFieldOrder(originalLines, explicitBlockedFalse, clusterFieldOrder)

	return &formatPreservationData{
		originalClusters:     originalClusters,
		explicitBlockedFalse: explicitBlockedFalse,
		clusterFieldOrder:    clusterFieldOrder,
		providerOrder:        providerOrder,
	}, nil
}

// extractProviderOrder determines the original order of providers in the YAML
func extractProviderOrder(lines []string) []string {
	var providerOrder []string
	seen := make(map[string]bool)

	for _, line := range lines {
		if len(line) > 0 && line[0] != ' ' && line[0] != '-' && strings.HasSuffix(line, ":") {
			provider := strings.TrimSuffix(line, ":")
			if !seen[provider] {
				providerOrder = append(providerOrder, provider)
				seen[provider] = true
			}
		}
	}
	return providerOrder
}

// parseFieldOrder analyzes field order and explicit blocked entries for each cluster
func parseFieldOrder(lines []string, explicitBlockedFalse map[string]bool, clusterFieldOrder map[string][]string) {
	currentCluster := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- name: ") {
			currentCluster = strings.TrimPrefix(trimmed, "- name: ")
			clusterFieldOrder[currentCluster] = []string{"name"}
		} else if currentCluster != "" && strings.HasPrefix(line, "    ") {
			addFieldToOrder(line, currentCluster, explicitBlockedFalse, clusterFieldOrder)
		}
	}
}

// addFieldToOrder adds field to the order based on line content
func addFieldToOrder(line, currentCluster string, explicitBlockedFalse map[string]bool, clusterFieldOrder map[string][]string) {
	switch {
	case strings.Contains(line, "blocked: false"):
		explicitBlockedFalse[currentCluster] = true
		clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "blocked")
	case strings.Contains(line, "blocked: true"):
		clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "blocked")
	case strings.Contains(line, "capacity:"):
		clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "capacity")
	case strings.Contains(line, "capabilities:"):
		clusterFieldOrder[currentCluster] = append(clusterFieldOrder[currentCluster], "capabilities")
	}
}

// writeExistingClusters writes clusters from the original file, preserving their structure
func writeExistingClusters(output *strings.Builder, formatData *formatPreservationData, clusterMap ClusterMap, blockedClusters sets.Set[string]) map[string]bool {
	processedClusters := make(map[string]bool)

	for _, provider := range formatData.providerOrder {
		output.WriteString(fmt.Sprintf("%s:\n", provider))
		clusters := formatData.originalClusters[provider]

		for _, originalCluster := range clusters {
			processedClusters[originalCluster.Name] = true
			output.WriteString(fmt.Sprintf("  - name: %s\n", originalCluster.Name))

			if clusterInfo, exists := clusterMap[originalCluster.Name]; exists {
				writeClusterFields(output, originalCluster, clusterInfo, formatData, blockedClusters)
			} else {
				writeOriginalClusterFields(output, originalCluster, formatData)
			}
		}
	}
	return processedClusters
}

// writeClusterFields writes cluster fields for existing clusters in ClusterMap
func writeClusterFields(output *strings.Builder, original originalClusterInfo, cluster ClusterInfo, formatData *formatPreservationData, blockedClusters sets.Set[string]) {
	fieldOrder := getFieldOrder(original.Name, formatData.clusterFieldOrder)

	for _, field := range fieldOrder {
		switch field {
		case "capacity":
			writeCapacityField(output, original.Capacity, cluster.Capacity)
		case "capabilities":
			writeCapabilitiesField(output, cluster.Capabilities)
		case "blocked":
			writeBlockedField(output, original.Name, blockedClusters, formatData.explicitBlockedFalse)
		}
	}
}

// writeOriginalClusterFields writes fields for clusters not in ClusterMap (marked as blocked)
func writeOriginalClusterFields(output *strings.Builder, original originalClusterInfo, formatData *formatPreservationData) {
	fieldOrder := getFieldOrder(original.Name, formatData.clusterFieldOrder)

	for _, field := range fieldOrder {
		switch field {
		case "capacity":
			if original.Capacity > 0 {
				output.WriteString(fmt.Sprintf("    capacity: %d\n", original.Capacity))
			}
		case "capabilities":
			writeCapabilitiesField(output, original.Capabilities)
		case "blocked":
			output.WriteString("    blocked: true\n")
		}
	}
}

// getFieldOrder returns the field order for a cluster, with a default fallback
func getFieldOrder(clusterName string, clusterFieldOrder map[string][]string) []string {
	if order := clusterFieldOrder[clusterName]; len(order) > 0 {
		return order
	}
	return []string{"name", "capacity", "capabilities", "blocked"}
}

// writeCapacityField writes capacity field if needed
func writeCapacityField(output *strings.Builder, originalCapacity, currentCapacity int) {
	if originalCapacity > 0 || (currentCapacity != 100 && currentCapacity != 0) {
		capacity := currentCapacity
		if capacity == 0 || capacity == 100 {
			if originalCapacity > 0 {
				capacity = originalCapacity
			}
		}
		if capacity > 0 && capacity != 100 {
			output.WriteString(fmt.Sprintf("    capacity: %d\n", capacity))
		}
	}
}

// writeCapabilitiesField writes capabilities field if present
func writeCapabilitiesField(output *strings.Builder, capabilities []string) {
	if len(capabilities) > 0 {
		output.WriteString("    capabilities:\n")
		for _, cap := range capabilities {
			output.WriteString(fmt.Sprintf("    - %s\n", cap))
		}
	}
}

// writeBlockedField writes blocked field based on cluster state
func writeBlockedField(output *strings.Builder, clusterName string, blockedClusters sets.Set[string], explicitBlockedFalse map[string]bool) {
	if blockedClusters.Has(clusterName) {
		output.WriteString("    blocked: true\n")
	} else if explicitBlockedFalse[clusterName] {
		output.WriteString("    blocked: false\n")
	}
}

// writeNewClusters adds clusters that weren't in the original file
func writeNewClusters(output *strings.Builder, clusterMap ClusterMap, blockedClusters sets.Set[string], processedClusters map[string]bool) {
	newClusters := groupNewClustersByProvider(clusterMap, processedClusters)

	for provider, clusterNames := range newClusters {
		ensureProviderSection(output, provider)
		writeNewClusterEntries(output, clusterNames, clusterMap, blockedClusters)
	}
}

// groupNewClustersByProvider groups unprocessed clusters by their provider
func groupNewClustersByProvider(clusterMap ClusterMap, processedClusters map[string]bool) map[string][]string {
	newClusters := make(map[string][]string)
	for clusterName, clusterInfo := range clusterMap {
		if !processedClusters[clusterName] {
			newClusters[clusterInfo.Provider] = append(newClusters[clusterInfo.Provider], clusterName)
		}
	}
	return newClusters
}

// ensureProviderSection adds provider section if it doesn't exist
func ensureProviderSection(output *strings.Builder, provider string) {
	if !strings.Contains(output.String(), provider+":") {
		output.WriteString(fmt.Sprintf("\n%s:\n", provider))
	}
}

// writeNewClusterEntries writes entries for new clusters
func writeNewClusterEntries(output *strings.Builder, clusterNames []string, clusterMap ClusterMap, blockedClusters sets.Set[string]) {
	for _, clusterName := range clusterNames {
		clusterInfo := clusterMap[clusterName]
		output.WriteString(fmt.Sprintf("  - name: %s\n", clusterName))

		writeCapacityField(output, 0, clusterInfo.Capacity)
		writeCapabilitiesField(output, clusterInfo.Capabilities)

		if blockedClusters.Has(clusterName) {
			output.WriteString("    blocked: true\n")
		}
	}
}
