package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

const (
	BigQueryProjectID   = "openshift-ci-data-analysis"
	CIDataSetID         = "ci_data"
	JobsTableName       = "Jobs"
	JobRunTableName     = "JobRuns"
	TestRunTableName    = "TestRuns"
	PerDayTestRunTable  = "TestRunSummaryPerDay"
	PerWeekTestRunTable = "TestRunSummaryPerWeek"

	ReleaseTableName             = "ReleaseTags"
	ReleaseRepositoryTableName   = "ReleaseRepositories"
	ReleaseJobRunTableName       = "ReleaseJobRuns"
	ReleasePullRequestsTableName = "ReleasePullRequests"
)

type BigQueryDataCoordinates struct {
	ProjectID string
	DataSetID string
}

func NewBigQueryDataCoordinates() *BigQueryDataCoordinates {
	return &BigQueryDataCoordinates{
		ProjectID: BigQueryProjectID,
		DataSetID: CIDataSetID,
	}
}

func (f *BigQueryDataCoordinates) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.ProjectID, "google-project-id", f.ProjectID, "project ID where data is stored")
	fs.StringVar(&f.DataSetID, "bigquery-dataset", f.DataSetID, "bigquery dataset where data is stored")
}

func (f *BigQueryDataCoordinates) Validate() error {
	if len(f.ProjectID) == 0 {
		return fmt.Errorf("one of --google-service-account-credential-file or --google-oauth-credential-file must be specified")
	}
	if len(f.DataSetID) == 0 {
		return fmt.Errorf("one of --google-service-account-credential-file or --google-oauth-credential-file must be specified")
	}

	return nil
}

func (f *BigQueryDataCoordinates) SubstituteDataSetLocation(query string) string {
	return strings.Replace(
		query,
		"DATA_SET_LOCATION",
		f.ProjectID+"."+f.DataSetID,
		-1)
}

type BigQueryInserter interface {
	Put(ctx context.Context, src interface{}) (err error)
}
