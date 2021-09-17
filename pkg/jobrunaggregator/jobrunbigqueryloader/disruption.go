package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

type availabilityResult struct {
	serverName         string
	secondsUnavailable int
}

func getServerAvailabilityResults(suites *junit.TestSuites) map[string]availabilityResult {
	availabilityResultsByName := map[string]availabilityResult{}

	for _, curr := range suites.Suites {
		currResults := getServerAvailabilityResultsBySuite(curr)
		addUnavailability(availabilityResultsByName, currResults)
	}

	return availabilityResultsByName
}

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

func getServerAvailabilityResultsBySuite(suite *junit.TestSuite) map[string]availabilityResult {
	availabilityResultsByName := map[string]availabilityResult{}

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
			availabilityResultsByName[backendName] = availabilityResult{
				serverName:         backendName,
				secondsUnavailable: 0,
			}
		}
	}

	return availabilityResultsByName
}

func addUnavailabilityForAPIServerTest(runningTotals map[string]availabilityResult, serverName string, message string) {
	secondsUnavailable, err := getOutageSecondsFromMessage(message)
	if err != nil {
		fmt.Printf("#### err %v\n", err)
		return
	}
	existing := runningTotals[serverName]
	existing.secondsUnavailable += secondsUnavailable
	runningTotals[serverName] = existing
}

func addUnavailability(runningTotals, toAdd map[string]availabilityResult) {
	for serverName, unavailability := range toAdd {
		existing := runningTotals[serverName]
		existing.secondsUnavailable += unavailability.secondsUnavailable
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

type disruptionUploader struct {
	backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter
}

func newDisruptionUploader(backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter) uploader {
	return &disruptionUploader{
		backendDisruptionInserter: backendDisruptionInserter,
	}
}

func (o *disruptionUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) error {
	fmt.Printf("  uploading backend disruption results: %q/%q\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	combinedJunitContent, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return err
	}

	return o.uploadBackendDisruption(ctx, jobRun.GetJobName(), jobRun.GetJobRunID(), combinedJunitContent)
}

func (o *disruptionUploader) uploadBackendDisruption(ctx context.Context, jobName, jobRunName string, suites *junit.TestSuites) error {
	rows := []*jobrunaggregatorapi.BackendDisruptionRow{}
	serverAvailabilityResults := getServerAvailabilityResults(suites)
	for _, backendName := range sets.StringKeySet(serverAvailabilityResults).List() {
		unavailability := serverAvailabilityResults[backendName]
		row := &jobrunaggregatorapi.BackendDisruptionRow{
			BackendName:       backendName,
			JobRunName:        jobRunName,
			DisruptionSeconds: unavailability.secondsUnavailable,
		}
		rows = append(rows, row)
	}
	if err := o.backendDisruptionInserter.Put(ctx, rows); err != nil {
		return err
	}
	return nil
}
