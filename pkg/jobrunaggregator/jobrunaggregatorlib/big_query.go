package jobrunaggregatorlib

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

const (
	BigQueryProjectID = "openshift-ci-data-analysis"
	CIDataSetID       = "ci_data"

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
	return strings.ReplaceAll(
		query,
		"DATA_SET_LOCATION",
		f.ProjectID+"."+f.DataSetID)
}

type BigQueryInserter interface {
	Put(ctx context.Context, src interface{}) (err error)
}

type dryRunInserter struct {
	table string
	out   io.Writer
}

func NewDryRunInserter(out io.Writer, table string) BigQueryInserter {
	return dryRunInserter{
		table: table,
		out:   out,
	}
}

func (d dryRunInserter) Put(ctx context.Context, src interface{}) (err error) {
	srcVal := reflect.ValueOf(src)
	if srcVal.Kind() != reflect.Slice {
		logrus.Debugf("INSERT into %s: %v", d.table, src)
		return
	}

	if srcVal.Len() == 0 {
		return
	}

	// Accumulate bulk insert debugging into a buffer so it's not mixed with concurrent logging from other goroutines:
	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, "BULK INSERT into %v\n", d.table)
	for i := 0; i < srcVal.Len(); i++ {

		switch s := srcVal.Index(i).Interface().(type) {
		case *jobrunaggregatorapi.JobRunRow:
			fmt.Fprintf(buf, "\tINSERT into %v: name=%v, jobname=%v, status=%v\n", d.table, s.Name, s.JobName, s.Status)

		case *jobrunaggregatorapi.BackendDisruptionRow:
			fmt.Fprintf(buf, "\tINSERT into %v: %#v\n", d.table, s)

		case jobrunaggregatorapi.JobRow:
			fmt.Fprintf(buf, "\tINSERT into %v: JobName=%v\n", d.table, s.JobName)

		default:
			// If we don't know the type, output something generic.
			fmt.Fprintf(buf, "\tINSERT into %v: %#v\n", d.table, s)
		}
	}
	logrus.Debug(buf.String())

	return nil
}
