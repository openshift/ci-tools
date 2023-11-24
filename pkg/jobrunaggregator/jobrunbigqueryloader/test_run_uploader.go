package jobrunbigqueryloader

import (
	"context"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type testRunUploader struct {
	testRunInserter jobrunaggregatorlib.BigQueryInserter
	ciDataClient    jobrunaggregatorlib.CIDataClient
}

type testRunPendingUploadLister struct {
	tableName    string
	ciDataClient jobrunaggregatorlib.CIDataClient
}

func newTestRunPendingUploadLister(ciDataClient jobrunaggregatorlib.CIDataClient) pendingUploadLister {
	return &testRunPendingUploadLister{
		tableName:    jobrunaggregatorapi.LegacyJobRunTableName,
		ciDataClient: ciDataClient,
	}
}

func newTestRunUploader(testRunInserter jobrunaggregatorlib.BigQueryInserter,
	ciDataClient jobrunaggregatorlib.CIDataClient) uploader {
	return &testRunUploader{
		testRunInserter: testRunInserter,
		ciDataClient:    ciDataClient,
	}
}

func (o *testRunPendingUploadLister) getLastUploadedJobRunEndTime(ctx context.Context) (*time.Time, error) {
	return o.ciDataClient.GetLastJobRunEndTimeFromTable(ctx, o.tableName)
}

func (o *testRunPendingUploadLister) listUploadedJobRunIDsSince(ctx context.Context, since *time.Time) (map[string]bool, error) {
	return o.ciDataClient.ListUploadedJobRunIDsSinceFromTable(ctx, o.tableName, since)
}

func (o *testRunUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, jobRelease string,
	jobRunRow *jobrunaggregatorapi.JobRunRow, logger logrus.FieldLogger) error {

	logger.Info("uploading junit test runs")
	combinedJunitContent, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return err
	}

	return o.uploadTestSuites(ctx, jobRunRow, combinedJunitContent)
}

func (o *testRunUploader) uploadTestSuites(ctx context.Context, jobRunRow *jobrunaggregatorapi.JobRunRow, suites *junit.TestSuites) error {

	for _, testSuite := range suites.Suites {
		if err := o.uploadTestSuite(ctx, jobRunRow, []string{}, testSuite); err != nil {
			return err
		}
	}
	return nil
}

func (o *testRunUploader) uploadTestSuite(ctx context.Context, jobRunRow *jobrunaggregatorapi.JobRunRow, parentSuites []string, suite *junit.TestSuite) error { // nolint
	currSuites := append(parentSuites, suite.Name)
	for _, testSuite := range suite.Children {
		if err := o.uploadTestSuite(ctx, jobRunRow, currSuites, testSuite); err != nil {
			return err
		}
	}

	toInsert := []*jobrunaggregatorapi.TestRunRow{}
	for i := range suite.TestCases {
		testCase := suite.TestCases[i]
		if testCase.SkipMessage != nil {
			continue
		}

		var status string
		switch {
		case testCase.FailureOutput != nil:
			status = "Failed"
		case testCase.SkipMessage != nil:
			status = "Skipped"
		default:
			status = "Passed"
		}

		testSuiteStr := strings.Join(currSuites, jobrunaggregatorlib.TestSuitesSeparator)
		toInsert = append(toInsert, newTestRunRow(jobRunRow, status, testSuiteStr, testCase))
	}
	if err := o.testRunInserter.Put(ctx, toInsert); err != nil {
		return err
	}

	return nil
}
