package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	fakeclock "k8s.io/utils/clock/testing"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
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

	tests := []struct {
		name              string
		jobRunInfos       []jobrunaggregatorapi.JobRunInfo
		expectErrContains string
	}{
		{
			name: "no jobs finished",
			jobRunInfos: []jobrunaggregatorapi.JobRunInfo{
				buildFakeJobRunInfo(mockCtrl, "1001", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1002", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1003", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1004", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1005", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1006", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1007", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1008", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1009", payloadStartTime),
				buildFakeJobRunInfo(mockCtrl, "1010", payloadStartTime),
			},
			expectErrContains: "found 10 unfinished related jobRuns",
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

			for _, ri := range tc.jobRunInfos {
				mockGCSClient.EXPECT().ReadJobRunFromGCS(gomock.Any(), gomock.Any(), testJobName, ri.GetJobRunID(), gomock.Any()).Return(ri, nil)
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
			if tc.expectErrContains != "" {
				assert.ErrorContains(t, err, tc.expectErrContains)
			} else {
				assert.NoError(t, err)
			}

		})
	}

}

func buildFakeJobRunInfo(mockCtrl *gomock.Controller,
	jobRunID string,
	payloadStartTime time.Time) jobrunaggregatorapi.JobRunInfo {

	prowJob := &prowjobv1.ProwJob{
		ObjectMeta: v1.ObjectMeta{CreationTimestamp: v1.NewTime(payloadStartTime)},
	}
	/*
		if finished {
			completionTime := v1.NewTime(payloadStartTime.Add(3 * time.Hour))
			prowJob.Status.CompletionTime = &completionTime
		}
	*/

	mockJRI := jobrunaggregatorapi.NewMockJobRunInfo(mockCtrl)
	// pass finished in when we're ready, damn linters...
	mockJRI.EXPECT().IsFinished(gomock.Any()).Return(false).AnyTimes()
	mockJRI.EXPECT().GetJobName().Return(testJobName).AnyTimes()
	mockJRI.EXPECT().GetJobRunID().Return(jobRunID).AnyTimes()
	mockJRI.EXPECT().GetHumanURL().Return("unused").AnyTimes()
	mockJRI.EXPECT().GetProwJob(gomock.Any()).Return(prowJob, nil).AnyTimes()
	return mockJRI
}
