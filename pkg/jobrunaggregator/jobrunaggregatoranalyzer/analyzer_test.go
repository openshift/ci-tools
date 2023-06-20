package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
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
	mockDataClient.EXPECT().GetJobRunForJobNameAfterTime(gomock.Any(), testJobName, endPayloadJobRunWindow).Return("2000", nil)

	mockGCSClient := jobrunaggregatorlib.NewMockCIGCSClient(mockCtrl)
	mockGCSClient.EXPECT().ReadRelatedJobRuns(
		gomock.Any(),
		testJobName,
		fmt.Sprintf("logs/%s", testJobName),
		"1000",
		"2000",
		gomock.Any()).Return([]jobrunaggregatorapi.JobRunInfo{
		buildFakeJobRunInfo(mockCtrl, false, testJobName, "1001", payloadStartTime),
	}, nil)

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

func buildFakeJobRunInfo(mockCtrl *gomock.Controller,
	finished bool,
	jobName,
	jobRunID string,
	payloadStartTime time.Time) jobrunaggregatorapi.JobRunInfo {

	prowJob := &prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{CreationTimestamp: v1.NewTime(payloadStartTime)},
	}

	mockJRI := jobrunaggregatorapi.NewMockJobRunInfo(mockCtrl)
	mockJRI.EXPECT().IsFinished(gomock.Any()).Return(finished)
	mockJRI.EXPECT().GetJobName().Return(jobName).AnyTimes()
	mockJRI.EXPECT().GetJobRunID().Return(jobRunID).AnyTimes()
	mockJRI.EXPECT().GetHumanURL().Return("unused").AnyTimes()
	mockJRI.EXPECT().GetProwJob(gomock.Any()).Return(prowJob, nil).AnyTimes()
	return mockJRI
}
