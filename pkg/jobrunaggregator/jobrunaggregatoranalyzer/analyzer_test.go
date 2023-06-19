package jobrunaggregatoranalyzer

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/stretchr/testify/assert"
	fakeclock "k8s.io/utils/clock/testing"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

const (
	testJobName    = "periodic-ci-openshift-release-master-ci-4.14-e2e-gcp-ovn-upgrade"
	testPayloadtag = "4.14.0-0.ci-2023-06-18-131345"
)

func TestAnalyzer(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	workDir, err := ioutil.TempDir("/tmp/", "ci-tools-aggregator-test-workdir")
	assert.NoError(t, err)
	defer os.RemoveAll(workDir)

	// Used for when we estimate the payload launched, as well as when the aggregator job
	// kicked off.
	payloadStartTime := time.Date(2023, 6, 18, 12, 0, 0, 0, time.UTC)

	// matches what we do in the JobRunLocator:
	startPayloadJobRunWindow := payloadStartTime.Add(-1 * jobrunaggregatorlib.JobSearchWindowStartOffset)
	endPayloadJobRunWindow := payloadStartTime.Add(jobrunaggregatorlib.JobSearchWindowEndOffset)

	mockDataClient := jobrunaggregatorlib.NewMockCIDataClient(mockCtrl)
	mockDataClient.EXPECT().GetJobRunForJobNameBeforeTime(gomock.Any(), testJobName, startPayloadJobRunWindow).Return("1000", nil)
	mockDataClient.EXPECT().GetJobRunForJobNameAfterTime(gomock.Any(), testJobName, endPayloadJobRunWindow).Return("1000", nil)

	mockGCSClient := jobrunaggregatorlib.NewMockCIGCSClient(mockCtrl)
	mockGCSClient.EXPECT().ReadRelatedJobRuns(gomock.Any()).Return([]jobrunaggregatorapi.JobRunInfo{
		buildFakeJobRunInfo(mockCtrl, false),
	})

	analyzer := JobRunAggregatorAnalyzerOptions{
		jobRunLocator: jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
			testJobName,
			testPayloadtag,
			payloadStartTime,
			mockDataClient,
			mockGCSClient,
			"bucketname",
		),
		passFailCalculator:  nil,
		explicitGCSPrefix:   "",
		jobName:             testJobName,
		payloadTag:          testPayloadtag,
		workingDir:          workDir,
		jobRunStartEstimate: payloadStartTime,
		clock:               fakeclock.NewFakeClock(payloadStartTime),
		timeout:             6 * time.Hour,
	}
	err = analyzer.Run(context.TODO())
	assert.NoError(t, err)

}

func buildFakeJobRunInfo(mockCtrl *gomock.Controller, finished bool, jobName, jobRunID string) jobrunaggregatorapi.JobRunInfo {
	mockJRI := jobrunaggregatorapi.NewMockJobRunInfo(mockCtrl)
	return mockJRI
}
