package jobrunaggregatoranalyzer

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

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

func TestIsInformingTest(t *testing.T) {
	tests := []struct {
		name     string
		testCase *junit.TestCase
		expected bool
	}{
		{
			name:     "no lifecycle",
			testCase: &junit.TestCase{Name: "test"},
			expected: false,
		},
		{
			name:     "lifecycle informing",
			testCase: &junit.TestCase{Name: "test", Lifecycle: "informing"},
			expected: true,
		},
		{
			name:     "lifecycle blocking",
			testCase: &junit.TestCase{Name: "test", Lifecycle: "blocking"},
			expected: false,
		},
		{
			name:     "lifecycle empty",
			testCase: &junit.TestCase{Name: "test", Lifecycle: ""},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isInformingTest(tc.testCase))
		})
	}
}

func TestHasBlockingFailedTestCase(t *testing.T) {
	tests := []struct {
		name     string
		suite    *junit.TestSuite
		expected bool
	}{
		{
			name: "no failures",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{Name: "passing test"},
				},
			},
			expected: false,
		},
		{
			name: "non-informing failure",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{Name: "failing test", FailureOutput: &junit.FailureOutput{Message: "failed"}},
				},
			},
			expected: true,
		},
		{
			name: "informing failure only",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{
						Name:          "informing test",
						Lifecycle:     "informing",
						FailureOutput: &junit.FailureOutput{Message: "failed"},
					},
				},
			},
			expected: false,
		},
		{
			name: "informing and non-informing failures",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{
						Name:          "informing test",
						Lifecycle:     "informing",
						FailureOutput: &junit.FailureOutput{Message: "failed"},
					},
					{Name: "blocking test", FailureOutput: &junit.FailureOutput{Message: "failed"}},
				},
			},
			expected: true,
		},
		{
			name: "informing failure in child suite only",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{Name: "passing test"},
				},
				Children: []*junit.TestSuite{
					{
						TestCases: []*junit.TestCase{
							{
								Name:          "informing test",
								Lifecycle:     "informing",
								FailureOutput: &junit.FailureOutput{Message: "failed"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "non-informing failure in child suite",
			suite: &junit.TestSuite{
				Children: []*junit.TestSuite{
					{
						TestCases: []*junit.TestCase{
							{Name: "failing test", FailureOutput: &junit.FailureOutput{Message: "failed"}},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, hasBlockingFailedTestCase(tc.suite))
		})
	}
}

func TestAggregateTestCasePropagatesLifecycle(t *testing.T) {
	combined := &junit.TestCase{Name: "test-with-lifecycle"}

	source := &junit.TestCase{
		Name:      "test-with-lifecycle",
		Lifecycle: "informing",
	}

	err := aggregateTestCase("suite", combined, "logs/job", "run1", source)
	assert.NoError(t, err)
	assert.Equal(t, "informing", combined.Lifecycle)

	// Second aggregation should not overwrite the lifecycle
	source2 := &junit.TestCase{
		Name:      "test-with-lifecycle",
		Lifecycle: "informing",
	}
	err = aggregateTestCase("suite", combined, "logs/job", "run2", source2)
	assert.NoError(t, err)
	assert.Equal(t, "informing", combined.Lifecycle)
}

func TestAggregateTestCasePropagatesProperties(t *testing.T) {
	combined := &junit.TestCase{Name: "test-with-properties"}

	source := &junit.TestCase{
		Name: "test-with-properties",
		Properties: []*junit.Property{
			{Name: "lifecycle", Value: "informing"},
			{Name: "owner", Value: "team-platform"},
		},
	}

	err := aggregateTestCase("suite", combined, "logs/job", "run1", source)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(combined.Properties))
	assert.Equal(t, "lifecycle", combined.Properties[0].Name)
	assert.Equal(t, "informing", combined.Properties[0].Value)

	// A different second aggregation should not overwrite the properties
	// (Usually the important properties would be the same anyway, but where properties differ, just one gets picked)
	source2 := &junit.TestCase{
		Name: "test-with-properties",
		Properties: []*junit.Property{
			{Name: "lifecycle", Value: "blocking"},
		},
	}
	err = aggregateTestCase("suite", combined, "logs/job", "run2", source2)
	assert.NoError(t, err)
	// Properties should remain unchanged from first aggregation
	assert.Equal(t, 2, len(combined.Properties))
	assert.Equal(t, "lifecycle", combined.Properties[0].Name)
	assert.Equal(t, "informing", combined.Properties[0].Value)
}

func TestInformingTestFailureMessage(t *testing.T) {
	suite := &junit.TestSuites{
		Suites: []*junit.TestSuite{
			{
				Name: "e2e-tests",
				TestCases: []*junit.TestCase{
					{
						Name:      "informing-test",
						Lifecycle: "informing",
						SystemOut: "name: informing-test\nfailures:\n- jobrunid: run1\n",
					},
				},
			},
		},
	}

	// Use a mock that always fails tests
	err := assignPassFail(context.TODO(), "test-job", suite, &alwaysFailBaseline{})
	assert.NoError(t, err)

	tc := suite.Suites[0].TestCases[0]
	assert.NotNil(t, tc.FailureOutput)
	assert.Contains(t, tc.FailureOutput.Message, "*** NON-BLOCKING FAILURE:")
	assert.Contains(t, tc.FailureOutput.Message, "lifecycle is 'informing'")
}

func TestInformingTestAnalyzer(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	payloadStartTime := time.Date(2023, 6, 18, 12, 0, 0, 0, time.UTC)

	// Build job runs with an informing test that fails
	junitXML := `<testsuites>
<testsuite tests="1" failures="0" time="1983" name="e2e-tests">
  <testcase name="informing-test" lifecycle="informing"/>
</testsuite>
</testsuites>`

	backendDisruptionJSON, _ := json.Marshal(jobrunaggregatorlib.BackendDisruptionList{
		BackendDisruptions: map[string]*jobrunaggregatorlib.BackendDisruption{},
	})
	openshiftFiles := map[string]string{"0": string(backendDisruptionJSON)}

	var jobRunInfos []jobrunaggregatorapi.JobRunInfo
	for i := 1001; i <= 1010; i++ {
		jobRunInfos = append(jobRunInfos, buildFakeJobRunInfo(mockCtrl, fmt.Sprintf("%d", i), payloadStartTime, true, junitXML, openshiftFiles))
	}

	workDir, err := os.MkdirTemp("/tmp/", "ci-tools-aggregator-test-informing")
	assert.NoError(t, err)
	defer os.RemoveAll(workDir)

	startPayloadJobRunWindow := payloadStartTime.Add(-1 * jobrunaggregatorlib.JobSearchWindowStartOffset)
	endPayloadJobRunWindow := payloadStartTime.Add(jobrunaggregatorlib.JobSearchWindowEndOffset)

	mockDataClient := jobrunaggregatorlib.NewMockCIDataClient(mockCtrl)
	mockDataClient.EXPECT().GetJobRunForJobNameBeforeTime(gomock.Any(), testJobName, startPayloadJobRunWindow).Return("1000", nil).Times(1)
	mockDataClient.EXPECT().GetJobRunForJobNameAfterTime(gomock.Any(), testJobName, endPayloadJobRunWindow).Return("2000", nil).Times(1)
	mockDataClient.EXPECT().GetJobVariants(gomock.Any(), gomock.Any()).Return(&jobrunaggregatorapi.JobRowWithVariants{Topology: "ha"}, nil).AnyTimes()

	mockGCSClient := jobrunaggregatorlib.NewMockCIGCSClient(mockCtrl)
	mockGCSClient.EXPECT().ReadRelatedJobRuns(
		gomock.Any(), testJobName, fmt.Sprintf("logs/%s", testJobName), "1000", "2000", gomock.Any(),
	).Return(jobRunInfos, nil).Times(1)

	var jobRunIDs []string
	for _, ri := range jobRunInfos {
		mockGCSClient.EXPECT().ReadJobRunFromGCS(gomock.Any(), gomock.Any(), testJobName, ri.GetJobRunID(), gomock.Any()).Return(ri, nil)
		jobRunIDs = append(jobRunIDs, ri.GetJobRunID())
	}

	// Use a calculator that fails the informing test
	analyzer := JobRunAggregatorAnalyzerOptions{
		jobRunLocator: jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
			testJobName, testPayloadtag, payloadStartTime, mockDataClient, mockGCSClient, "bucketname",
		),
		passFailCalculator:  &alwaysFailBaseline{jobRunIDs: jobRunIDs},
		ciDataClient:        mockDataClient,
		jobName:             testJobName,
		payloadTag:          testPayloadtag,
		workingDir:          workDir,
		jobRunStartEstimate: payloadStartTime,
		clock:               fakeclock.NewFakeClock(payloadStartTime),
		timeout:             6 * time.Hour,
	}

	// Should succeed because only informing tests failed
	err = analyzer.Run(context.TODO())
	assert.NoError(t, err, "aggregation should succeed when only informing tests fail")
}

// alwaysFailBaseline is a test baseline that always reports tests as failed.
type alwaysFailBaseline struct {
	jobRunIDs []string
}

func (b *alwaysFailBaseline) CheckFailed(_ context.Context, _ string, _ []string, details *jobrunaggregatorlib.TestCaseDetails) (testCaseStatus, string, error) {
	if !didTestRun(details) {
		return testCasePassed, "did not run", nil
	}
	return testCaseFailed, "always fails for testing", nil
}

func (b *alwaysFailBaseline) CheckDisruptionMeanWithinFiveStandardDeviations(_ context.Context, _ map[string]jobrunaggregatorlib.AvailabilityResult, _, _ string) ([]string, []string, testCaseStatus, string, error) {
	return []string{}, b.jobRunIDs, testCasePassed, "", nil
}

func (b *alwaysFailBaseline) CheckDisruptionMeanWithinOneStandardDeviation(_ context.Context, _ map[string]jobrunaggregatorlib.AvailabilityResult, _, _ string) ([]string, []string, testCaseStatus, string, error) {
	return []string{}, b.jobRunIDs, testCasePassed, "", nil
}

func (b *alwaysFailBaseline) CheckPercentileDisruption(_ context.Context, _ map[string]jobrunaggregatorlib.AvailabilityResult, _ string, _ int, _ int, _ string) ([]string, []string, testCaseStatus, string, error) {
	return []string{}, b.jobRunIDs, testCasePassed, "", nil
}

func TestCollectAllJobRunIDs(t *testing.T) {
	tests := []struct {
		name     string
		suite    *junit.TestSuite
		expected []string
	}{
		{
			name: "collects from failures, passes, and skips",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{
						SystemOut: "failures:\n- jobrunid: run-3\npasses:\n- jobrunid: run-1\nskips:\n- jobrunid: run-2\n",
					},
				},
			},
			expected: []string{"run-1", "run-2", "run-3"},
		},
		{
			name: "deduplicates across test cases",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{
						SystemOut: "failures:\n- jobrunid: run-1\n",
					},
					{
						SystemOut: "failures:\n- jobrunid: run-1\npasses:\n- jobrunid: run-2\n",
					},
				},
			},
			expected: []string{"run-1", "run-2"},
		},
		{
			name: "collects from child suites",
			suite: &junit.TestSuite{
				TestCases: []*junit.TestCase{
					{
						SystemOut: "passes:\n- jobrunid: run-1\n",
					},
				},
				Children: []*junit.TestSuite{
					{
						TestCases: []*junit.TestCase{
							{
								SystemOut: "passes:\n- jobrunid: run-2\n",
							},
						},
					},
				},
			},
			expected: []string{"run-1", "run-2"},
		},
		{
			name:     "empty suite",
			suite:    &junit.TestSuite{},
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := collectAllJobRunIDs(tc.suite)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestHtmlForTestRunsContainsJobNumbers(t *testing.T) {
	suite := &junit.TestSuite{
		Name: "test-suite",
		TestCases: []*junit.TestCase{
			{
				Name:          "failing-test",
				FailureOutput: &junit.FailureOutput{Message: "failed"},
				SystemOut:     "name: failing-test\nfailures:\n- jobrunid: run-b\n  humanurl: https://example.com/run-b\n- jobrunid: run-a\n  humanurl: https://example.com/run-a\n",
			},
		},
	}

	html, err := htmlForTestRuns("test-job", suite)
	assert.NoError(t, err)
	// run-a sorts before run-b, so run-a=1, run-b=2
	assert.Contains(t, html, "<p>Number: 1</p>")
	assert.Contains(t, html, "<p>Number: 2</p>")
}
