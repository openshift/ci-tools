package jobrunbigqueryloader

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
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
		"cluster-ingress-new-connections":                   "Cluster frontend ingress remain available",
		"ingress-to-oauth-server-new-connections":           "OAuth remains available via cluster frontend ingress using new connections",
		"ingress-to-oauth-server-used-connections":          "OAuth remains available via cluster frontend ingress using reused connections",
		"ingress-to-console-new-connections":                "Console remains available via cluster frontend ingress using new connections",
		"ingress-to-console-used-connections":               "Console remains available via cluster frontend ingress using reused connections",
	}

	e2eBackendNameToTestSubstring = map[string]string{
		"kube-api-new-connections":         "kube-apiserver-new-connection",
		"kube-api-reused-connections":      "kube-apiserver-reused-connection should be available",
		"openshift-api-new-connections":    "openshift-apiserver-new-connection should be available",
		"openshift-api-reused-connections": "openshift-apiserver-reused-connection should be available",
		"oauth-api-new-connections":        "oauth-apiserver-new-connection should be available",
		"oauth-api-reused-connections":     "oauth-apiserver-reused-connection should be available",
	}

	detectUpgradeOutage = regexp.MustCompile(` unreachable during disruption.*for at least (?P<DisruptionDuration>.*) of `)
	detectE2EOutage     = regexp.MustCompile(` was failing for (?P<DisruptionDuration>.*) seconds `)
)

func getServerAvailabilityResultsFromJunit(suites *junit.TestSuites) map[string]jobrunaggregatorlib.AvailabilityResult {
	availabilityResultsByName := map[string]jobrunaggregatorlib.AvailabilityResult{}

	for _, curr := range suites.Suites {
		currResults := getServerAvailabilityResultsBySuite(curr)
		addUnavailability(availabilityResultsByName, currResults)
	}

	return availabilityResultsByName
}

func getServerAvailabilityResultsBySuite(suite *junit.TestSuite) map[string]jobrunaggregatorlib.AvailabilityResult {
	availabilityResultsByName := map[string]jobrunaggregatorlib.AvailabilityResult{}

	for _, curr := range suite.Children {
		currResults := getServerAvailabilityResultsBySuite(curr)
		addUnavailability(availabilityResultsByName, currResults)
	}

	for _, testCase := range suite.TestCases {
		backendName := ""
		for currBackendName, testSubstring := range upgradeBackendNameToTestSubstring {
			if strings.Contains(testCase.Name, testSubstring) {
				backendName = currBackendName
				break
			}
		}
		for currBackendName, testSubstring := range e2eBackendNameToTestSubstring {
			if strings.Contains(testCase.Name, testSubstring) {
				backendName = currBackendName
				break
			}
		}
		if len(backendName) == 0 {
			continue
		}

		if testCase.FailureOutput != nil {
			addUnavailabilityForAPIServerTest(availabilityResultsByName, backendName, testCase.FailureOutput.Message)
			continue
		}

		// if the test passed and we DO NOT have an entry already, add one
		if _, ok := availabilityResultsByName[backendName]; !ok {
			availabilityResultsByName[backendName] = jobrunaggregatorlib.AvailabilityResult{
				ServerName:         backendName,
				SecondsUnavailable: 0,
			}
		}
	}

	return availabilityResultsByName
}

func addUnavailabilityForAPIServerTest(runningTotals map[string]jobrunaggregatorlib.AvailabilityResult, serverName string, message string) {
	secondsUnavailable, err := getOutageSecondsFromMessage(message)
	if err != nil {
		fmt.Printf("#### err %v\n", err)
		return
	}
	existing := runningTotals[serverName]
	existing.SecondsUnavailable += secondsUnavailable
	runningTotals[serverName] = existing
}

func addUnavailability(runningTotals, toAdd map[string]jobrunaggregatorlib.AvailabilityResult) {
	for serverName, unavailability := range toAdd {
		existing := runningTotals[serverName]
		existing.SecondsUnavailable += unavailability.SecondsUnavailable
		runningTotals[serverName] = existing
	}
}

func getOutageSecondsFromMessage(message string) (int, error) {
	matches := detectUpgradeOutage.FindStringSubmatch(message)
	if len(matches) < 2 {
		matches = detectE2EOutage.FindStringSubmatch(message)
	}
	if len(matches) < 2 {
		return 0, fmt.Errorf("not the expected format: %v", message)
	}
	outageDuration, err := time.ParseDuration(matches[1])
	if err != nil {
		return 0, err
	}
	return int(math.Ceil(outageDuration.Seconds())), nil
}
