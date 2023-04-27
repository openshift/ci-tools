package jobrunaggregatorlib

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	upgradeBackendNameToTestSubstring = map[string]string{
		"kube-api-new-connections":                          "Kubernetes APIs remain available for new connections",
		"kube-api-reused-connections":                       "Kubernetes APIs remain available with reused connections",
		"openshift-api-new-connections":                     "OpenShift APIs remain available for new connections",
		"openshift-api-reused-connections":                  "OpenShift APIs remain available with reused connections",
		"oauth-api-new-connections":                         "OAuth APIs remain available for new connections",
		"oauth-api-reused-connections":                      "OAuth APIs remain available with reused connections",
		"service-load-balancer-with-pdb-reused-connections": "Application behind service load balancer with PDB is not disrupted",
		"image-registry-reused-connections":                 "Image registry remain available",
		"ingress-to-oauth-server-new-connections":           "OAuth remains available via cluster frontend ingress using new connections",
		"ingress-to-oauth-server-used-connections":          "OAuth remains available via cluster frontend ingress using reused connections",
		"ingress-to-console-new-connections":                "Console remains available via cluster frontend ingress using new connections",
		"ingress-to-console-used-connections":               "Console remains available via cluster frontend ingress using reused connections",
	}
)

func RequiredDisruptionTests() sets.String {
	return sets.StringKeySet(upgradeBackendNameToTestSubstring)
}

type AvailabilityResult struct {
	ServerName         string
	SecondsUnavailable int
}

type BackendDisruptionList struct {
	// BackendDisruptions is keyed by name to make the consumption easier
	BackendDisruptions map[string]*BackendDisruption
}

type BackendDisruption struct {
	// Name ensure self-identification
	Name string
	// ConnectionType is New or Reused
	ConnectionType     string
	DisruptedDuration  v1.Duration
	DisruptionMessages []string
}

// ClusterData is defined in origin/platformidentification/types.go
// it is duplicated in a minimized form here
type ClusterData struct {
	MasterNodesUpdated string
}

// GetMasterNodesUpdatedStatusFromClusterData takes multiple file contents as a copy of the ClusterData
// file is created for multiple test phases (upgrade / conformance) in the same manor that multiple disruption
// files are created for the multiple phases
func GetMasterNodesUpdatedStatusFromClusterData(clusterData map[string]string) string {
	// default is unknown
	masterNodesUpdated := ""

	// there can be multiple files (upgrade / conformance) if any of them indicate the master nodes updated
	// we indicate that for the entire run
	for _, clusterdataResults := range clusterData {
		if len(clusterdataResults) == 0 {
			continue
		}

		cd := &ClusterData{}
		if err := json.Unmarshal([]byte(clusterdataResults), cd); err != nil {
			logrus.WithError(err).Error("error unmarshalling clusterdataJson")
			continue
		}

		// if the value is y then return it
		// as it supersedes all other values
		if strings.ToUpper(cd.MasterNodesUpdated) == "Y" {
			return cd.MasterNodesUpdated
		}

		// if we don't have a value yet use whatever value we have coming in
		if len(masterNodesUpdated) == 0 {
			masterNodesUpdated = cd.MasterNodesUpdated
			continue
		}
	}

	return masterNodesUpdated
}

func GetServerAvailabilityResultsFromDirectData(backendDisruptionData map[string]string) map[string]AvailabilityResult {
	availabilityResultsByName := map[string]AvailabilityResult{}

	for _, disruptionJSON := range backendDisruptionData {
		if len(disruptionJSON) == 0 {
			continue
		}
		allDisruptions := &BackendDisruptionList{}
		if err := json.Unmarshal([]byte(disruptionJSON), allDisruptions); err != nil {
			logrus.WithError(err).Error("error unmarshalling disruptionJson")
			continue
		}

		currAvailabilityResults := map[string]AvailabilityResult{}
		for _, disruption := range allDisruptions.BackendDisruptions {
			currAvailabilityResults[disruption.Name] = AvailabilityResult{
				ServerName:         disruption.Name,
				SecondsUnavailable: int(math.Ceil(disruption.DisruptedDuration.Seconds())),
			}
		}
		AddUnavailability(availabilityResultsByName, currAvailabilityResults)
	}

	return availabilityResultsByName
}

func AddUnavailability(runningTotals, toAdd map[string]AvailabilityResult) {
	for serverName, unavailability := range toAdd {
		existing := runningTotals[serverName]
		existing.SecondsUnavailable += unavailability.SecondsUnavailable
		runningTotals[serverName] = existing
	}
}
