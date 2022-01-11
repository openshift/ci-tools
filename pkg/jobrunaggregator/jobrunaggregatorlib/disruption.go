package jobrunaggregatorlib

import (
	"encoding/json"
	"math"

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

func GetServerAvailabilityResultsFromDirectData(backendDisruptionData map[string]string) map[string]AvailabilityResult {
	availabilityResultsByName := map[string]AvailabilityResult{}

	for _, disruptionJSON := range backendDisruptionData {
		if len(disruptionJSON) == 0 {
			continue
		}
		allDisruptions := &BackendDisruptionList{}
		if err := json.Unmarshal([]byte(disruptionJSON), allDisruptions); err != nil {
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
