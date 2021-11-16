package jobrunbigqueryloader

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type availabilityResult struct {
	serverName         string
	secondsUnavailable int
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
	DisruptedDuration  metav1.Duration
	DisruptionMessages []string
}

func getServerAvailabilityResultsFromDirectData(backendDisruptionData map[string]string) map[string]availabilityResult {
	availabilityResultsByName := map[string]availabilityResult{}

	for _, disruptionJSON := range backendDisruptionData {
		if len(disruptionJSON) == 0 {
			continue
		}
		allDisruptions := &BackendDisruptionList{}
		if err := json.Unmarshal([]byte(disruptionJSON), allDisruptions); err != nil {
			continue
		}

		currAvailabilityResults := map[string]availabilityResult{}
		for _, disruption := range allDisruptions.BackendDisruptions {
			currAvailabilityResults[disruption.Name] = availabilityResult{
				serverName:         disruption.Name,
				secondsUnavailable: int(math.Ceil(disruption.DisruptedDuration.Seconds())),
			}
		}
		addUnavailability(availabilityResultsByName, currAvailabilityResults)
	}

	return availabilityResultsByName
}

func getServerAvailabilityResultsFromJunit(suites *junit.TestSuites) map[string]availabilityResult {
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
	backendDisruptionData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "backend-disruption")
	if err != nil {
		return err
	}
	if len(backendDisruptionData) > 0 {
		return o.uploadBackendDisruptionFromDirectData(ctx, jobRun.GetJobRunID(), backendDisruptionData)
	}

	dateWeStartedTrackingDirectDisruptionData, err := time.Parse(time.RFC3339, "2021-11-08T00:00:00Z")
	if err != nil {
		return err
	}
	// TODO fix better before we hit 4.20
	releaseHasDisruptionData := strings.Contains(jobRun.GetJobName(), "4.10") ||
		strings.Contains(jobRun.GetJobName(), "4.11") ||
		strings.Contains(jobRun.GetJobName(), "4.12") ||
		strings.Contains(jobRun.GetJobName(), "4.13") ||
		strings.Contains(jobRun.GetJobName(), "4.14") ||
		strings.Contains(jobRun.GetJobName(), "4.15") ||
		strings.Contains(jobRun.GetJobName(), "4.16") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.19")
	if releaseHasDisruptionData && prowJob.CreationTimestamp.After(dateWeStartedTrackingDirectDisruptionData) {
		fmt.Printf("  No disruption data found, returning: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
		// we  have no data, just return
		return nil
	}

	fmt.Printf("  missing direct backend disruption results, trying to read from junit: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	// if we don't have
	combinedJunitContent, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return err
	}

	return o.uploadBackendDisruptionFromJunit(ctx, jobRun.GetJobRunID(), combinedJunitContent)
}

func (o *disruptionUploader) uploadBackendDisruptionFromJunit(ctx context.Context, jobRunName string, suites *junit.TestSuites) error {
	serverAvailabilityResults := getServerAvailabilityResultsFromJunit(suites)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}

func (o *disruptionUploader) uploadBackendDisruptionFromDirectData(ctx context.Context, jobRunName string, backendDisruptionData map[string]string) error {
	serverAvailabilityResults := getServerAvailabilityResultsFromDirectData(backendDisruptionData)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}
func (o *disruptionUploader) uploadBackendDisruption(ctx context.Context, jobRunName string, serverAvailabilityResults map[string]availabilityResult) error {
	rows := []*jobrunaggregatorapi.BackendDisruptionRow{}
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
