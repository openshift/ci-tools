package jobrunaggregatoranalyzer

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclock "k8s.io/utils/clock/testing"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

const (
	testJobName    = "periodic-ci-openshift-release-master-ci-4.14-e2e-gcp-ovn-upgrade"
	testPayloadtag = "4.14.0-0.ci-2023-06-18-131345"
)

func TestParseStaticJobRunInfo(t *testing.T) {
	// example json and validation we can unmarshal
	staticJSON := `[{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676762997483114496"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676762998317780992"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676762999165030400"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763000020668416"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763000834363392"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763001673224192"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763002516279296"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763003355140096"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763004194000896"},{"JobName": "periodic-ci-openshift-release-master-ci-4.14-e2e-azure-ovn-upgrade","JobRunId":"1676763005460680704"}]`
	jobRunInfo, err := jobrunaggregatorlib.GetStaticJobRunInfo(staticJSON, "")
	if err != nil {
		t.Fatalf("Failed to parse static JobRunInfo json: %v", err)
	}
	assert.Equal(t, 10, len(jobRunInfo), "Invalid JobRunInfo length")
}

func TestAnalyzer(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// Used for when we estimate the payload launched, as well as when the aggregator job
	// kicked off.
	payloadStartTime := time.Date(2023, 6, 18, 12, 0, 0, 0, time.UTC)

	historicalDisruption := jobrunaggregatorlib.BackendDisruptionList{
		BackendDisruptions: map[string]*jobrunaggregatorlib.BackendDisruption{
			"cache-kube-api-new-connections": {
				Name:              "cache-kube-api-new-connections",
				ConnectionType:    "New",
				DisruptedDuration: v1.Duration{Duration: time.Second * 1},
				LoadBalancerType:  "external",
			},
			"cache-kube-api-reused-connections": {
				Name:              "cache-kube-api-reused-connections",
				ConnectionType:    "Reused",
				DisruptedDuration: v1.Duration{Duration: time.Second * 2},
				LoadBalancerType:  "external",
			},
			"openshift-api-http2-localhost-reused-connections": {
				Name:              "openshift-api-http2-localhost-reused-connections",
				ConnectionType:    "Reused",
				DisruptedDuration: v1.Duration{Duration: time.Second * 2},
				LoadBalancerType:  "localhost",
			},
		},
	}

	tests := []struct {
		name              string
		jobRunInfos       []jobrunaggregatorapi.JobRunInfo
		expectErrContains string
	}{
		{
			name: "no jobs finished",
			jobRunInfos: []jobrunaggregatorapi.JobRunInfo{
				buildFakeJobRunInfo(mockCtrl, "1001", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1002", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1003", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1004", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1005", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1006", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1007", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1008", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1009", payloadStartTime, false, "", map[string]string{}),
				buildFakeJobRunInfo(mockCtrl, "1010", payloadStartTime, false, "", map[string]string{}),
			},
			expectErrContains: "found 10 unfinished related jobRuns",
		},
		{
			name: "all jobs finished",
			jobRunInfos: buildJobRunInfos(mockCtrl, payloadStartTime, jobrunaggregatorlib.BackendDisruptionList{
				BackendDisruptions: map[string]*jobrunaggregatorlib.BackendDisruption{
					"cache-kube-api-new-connections": {
						Name:              "cache-kube-api-new-connections",
						ConnectionType:    "New",
						DisruptedDuration: v1.Duration{Duration: time.Second * 2},
						LoadBalancerType:  "external",
					},
					"cache-kube-api-reused-connections": {
						Name:              "cache-kube-api-reused-connections",
						ConnectionType:    "Reused",
						DisruptedDuration: v1.Duration{Duration: time.Second * 2},
						LoadBalancerType:  "external",
					},
				},
			}),
			expectErrContains: "",
		},
		{
			name: "too much disruption",
			jobRunInfos: buildJobRunInfos(mockCtrl, payloadStartTime, jobrunaggregatorlib.BackendDisruptionList{
				BackendDisruptions: map[string]*jobrunaggregatorlib.BackendDisruption{
					"cache-kube-api-new-connections": {
						Name:              "cache-kube-api-new-connections",
						ConnectionType:    "New",
						DisruptedDuration: v1.Duration{Duration: time.Second * 2},
						LoadBalancerType:  "external",
					},
					"cache-kube-api-reused-connections": {
						Name:              "cache-kube-api-reused-connections",
						ConnectionType:    "Reused",
						DisruptedDuration: v1.Duration{Duration: time.Second * 6},
						LoadBalancerType:  "external",
					},
				},
			}),
			expectErrContains: "Some tests failed aggregation",
		},
		{
			name: "ignore disruptions if matches job name",
			jobRunInfos: buildJobRunInfos(mockCtrl, payloadStartTime, jobrunaggregatorlib.BackendDisruptionList{
				BackendDisruptions: map[string]*jobrunaggregatorlib.BackendDisruption{
					"cache-kube-api-new-connections": {
						Name:              "cache-kube-api-new-connections",
						ConnectionType:    "New",
						DisruptedDuration: v1.Duration{Duration: time.Second * 2},
						LoadBalancerType:  "external",
					},
					"cache-kube-api-reused-connections": {
						Name:              "cache-kube-api-reused-connections",
						ConnectionType:    "Reused",
						DisruptedDuration: v1.Duration{Duration: time.Second * 2},
						LoadBalancerType:  "external",
					},
					"openshift-api-http2-localhost-reused-connections": {
						Name:              "openshift-api-http2-localhost-reused-connections",
						ConnectionType:    "Reused",
						DisruptedDuration: v1.Duration{Duration: time.Second * 10},
						LoadBalancerType:  "localhost",
					},
				},
			}),
			expectErrContains: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir, err := os.MkdirTemp("/tmp/", "ci-tools-aggregator-test-workdir")
			assert.NoError(t, err)
			defer os.RemoveAll(workDir)

			// matches what we do in the JobRunLocator:
			startPayloadJobRunWindow := payloadStartTime.Add(-1 * jobrunaggregatorlib.JobSearchWindowStartOffset)
			endPayloadJobRunWindow := payloadStartTime.Add(jobrunaggregatorlib.JobSearchWindowEndOffset)

			mockDataClient := jobrunaggregatorlib.NewMockCIDataClient(mockCtrl)
			mockDataClient.EXPECT().GetJobRunForJobNameBeforeTime(gomock.Any(), testJobName, startPayloadJobRunWindow).Return("1000", nil).Times(1)
			mockDataClient.EXPECT().GetJobRunForJobNameAfterTime(gomock.Any(), testJobName, endPayloadJobRunWindow).Return("2000", nil).Times(1)

			mockGCSClient := jobrunaggregatorlib.NewMockCIGCSClient(mockCtrl)
			mockGCSClient.EXPECT().ReadRelatedJobRuns(
				gomock.Any(),
				testJobName,
				fmt.Sprintf("logs/%s", testJobName),
				"1000",
				"2000",
				gomock.Any()).Return(tc.jobRunInfos, nil).Times(1)

			var jobRunIDs []string
			jobRowWithVariants := &jobrunaggregatorapi.JobRowWithVariants{
				Topology: "ha",
			}

			for _, ri := range tc.jobRunInfos {
				mockGCSClient.EXPECT().ReadJobRunFromGCS(gomock.Any(), gomock.Any(), testJobName, ri.GetJobRunID(), gomock.Any()).Return(ri, nil)

				mockDataClient.EXPECT().GetJobVariants(gomock.Any(), gomock.Any()).Return(jobRowWithVariants, nil).AnyTimes()
				jobRunIDs = append(jobRunIDs, ri.GetJobRunID())
			}

			analyzer := JobRunAggregatorAnalyzerOptions{
				jobRunLocator: jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
					testJobName,
					testPayloadtag,
					payloadStartTime,
					mockDataClient,
					mockGCSClient,
					"bucketname",
				),
				passFailCalculator:  NewMockPassFailCalculator(jobRunIDs, historicalDisruption),
				ciDataClient:        mockDataClient,
				explicitGCSPrefix:   "",
				jobName:             testJobName,
				payloadTag:          testPayloadtag,
				workingDir:          workDir,
				jobRunStartEstimate: payloadStartTime,
				clock:               fakeclock.NewFakeClock(payloadStartTime),
				timeout:             6 * time.Hour,
			}
			err = analyzer.Run(context.TODO())
			if tc.expectErrContains != "" {
				assert.ErrorContains(t, err, tc.expectErrContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}

}

func buildJobRunInfos(mockCtrl *gomock.Controller, payloadStartTime time.Time, disruption jobrunaggregatorlib.BackendDisruptionList) []jobrunaggregatorapi.JobRunInfo {
	backendDisruptionJSON, _ := json.Marshal(disruption)
	openshiftFiles := map[string]string{
		"0": string(backendDisruptionJSON),
	}
	junitXML := `<testsuites>
<testsuite tests="1" failures="0" time="1983" name="BackendDisruption">
</testsuite>
</testsuites>`
	return []jobrunaggregatorapi.JobRunInfo{
		buildFakeJobRunInfo(mockCtrl, "1001", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1002", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1003", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1004", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1005", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1006", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1007", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1008", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1009", payloadStartTime, true, junitXML, openshiftFiles),
		buildFakeJobRunInfo(mockCtrl, "1010", payloadStartTime, true, junitXML, openshiftFiles),
	}
}

func buildFakeJobRunInfo(mockCtrl *gomock.Controller,
	jobRunID string,
	payloadStartTime time.Time,
	finished bool,
	junitXML string,
	openshiftTestFiles map[string]string) jobrunaggregatorapi.JobRunInfo {

	prowJob := &prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{CreationTimestamp: v1.NewTime(payloadStartTime)},
	}
	mockJRI := jobrunaggregatorapi.NewMockJobRunInfo(mockCtrl)
	if finished {
		completionTime := v1.NewTime(payloadStartTime.Add(3 * time.Hour))
		prowJob.Status.CompletionTime = &completionTime

		mockJRI.EXPECT().IsFinished(gomock.Any()).Return(true).AnyTimes()
		mockJRI.EXPECT().GetJobRunFromGCS(gomock.Any()).Return(nil).AnyTimes()
		mockJRI.EXPECT().GetOpenShiftTestsFilesWithPrefix(gomock.Any(), gomock.Any()).Return(openshiftTestFiles, nil).AnyTimes()

		suites := &junit.TestSuites{}
		err := xml.Unmarshal([]byte(junitXML), suites)
		mockJRI.EXPECT().GetCombinedJUnitTestSuites(gomock.Any()).Return(suites, err).AnyTimes()

		mockJRI.EXPECT().GetGCSArtifactURL().Return("").AnyTimes()

	} else {
		// pass finished in when we're ready, damn linters...
		mockJRI.EXPECT().IsFinished(gomock.Any()).Return(false).AnyTimes()
	}
	mockJRI.EXPECT().GetJobName().Return(testJobName).AnyTimes()
	mockJRI.EXPECT().GetJobRunID().Return(jobRunID).AnyTimes()
	mockJRI.EXPECT().GetHumanURL().Return("unused").AnyTimes()
	mockJRI.EXPECT().GetProwJob(gomock.Any()).Return(prowJob, nil).AnyTimes()
	return mockJRI
}

type mockPassFailCalculator struct {
	jobRunIDs                    []string
	historicalBackendDisruptions jobrunaggregatorlib.BackendDisruptionList
}

func (m mockPassFailCalculator) CheckFailed(ctx context.Context, jobName string, suiteNames []string, testCaseDetails *jobrunaggregatorlib.TestCaseDetails) (status testCaseStatus, message string, err error) {
	return "", "", nil
}
func (m mockPassFailCalculator) CheckDisruptionMeanWithinFiveStandardDeviations(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend, masterNodesUpdated string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error) {
	return []string{}, m.jobRunIDs, "", "", nil
}
func (m mockPassFailCalculator) CheckDisruptionMeanWithinOneStandardDeviation(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult, backend, masterNodesUpdated string) (failedJobRunsIDs []string, successfulJobRunIDs []string, status testCaseStatus, message string, err error) {
	return []string{}, m.jobRunIDs, "", "", nil
}
func (m mockPassFailCalculator) CheckPercentileDisruption(ctx context.Context, jobRunIDToAvailabilityResultForBackend map[string]jobrunaggregatorlib.AvailabilityResult,
	backend string, percentile int, fixedGraceSeconds int, masterNodesUpdated string) (failureJobRunIDs []string, successJobRunIDs []string, status testCaseStatus, message string, err error) {
	failureJobRunIDs = []string{}
	successJobRunIDs = []string{}
	historicalDisruptions, ok := m.historicalBackendDisruptions.BackendDisruptions[backend]
	if !ok {
		return m.jobRunIDs, []string{}, "", "", nil
	}
	for _, runID := range m.jobRunIDs {
		result, ok := jobRunIDToAvailabilityResultForBackend[runID]
		if !ok {
			failureJobRunIDs = append(failureJobRunIDs, runID)
			continue
		}
		maxDisruption := time.Duration(fixedGraceSeconds)*time.Second + historicalDisruptions.DisruptedDuration.Duration
		if time.Duration(result.SecondsUnavailable)*time.Second > maxDisruption {
			failureJobRunIDs = append(failureJobRunIDs, runID)
			continue
		}
		successJobRunIDs = append(successJobRunIDs, runID)
	}
	status = "passed"
	message = ""
	if len(failureJobRunIDs) > 0 {
		status = "failed"
		message = fmt.Sprintf("disruption for %s should not be worse", backend)
	}
	return failureJobRunIDs, successJobRunIDs, status, message, nil
}

func NewMockPassFailCalculator(jobRunIDs []string, historicalBackendDisruptions jobrunaggregatorlib.BackendDisruptionList) mockPassFailCalculator {
	return mockPassFailCalculator{
		jobRunIDs:                    jobRunIDs,
		historicalBackendDisruptions: historicalBackendDisruptions,
	}
}
