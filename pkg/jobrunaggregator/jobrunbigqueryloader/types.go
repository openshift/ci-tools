package jobrunbigqueryloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"time"

	"cloud.google.com/go/bigquery"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

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
		fmt.Fprintf(d.out, "INSERT into %v: %v\n", d.table, src)
		return
	}

	if srcVal.Len() == 0 {
		return
	}

	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, "BULK INSERT into %v\n", d.table)
	for i := 0; i < srcVal.Len(); i++ {
		s := srcVal.Index(i).Interface()
		fmt.Fprintf(buf, "\tINSERT into %v: %v\n", d.table, s)
	}
	fmt.Fprint(d.out, buf.String())

	return nil
}

func newJobRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) *jobrunaggregatorapi.JobRunRow {
	var endTime time.Time
	if prowJob.Status.CompletionTime != nil {
		endTime = prowJob.Status.CompletionTime.Time
	}
	return &jobrunaggregatorapi.JobRunRow{
		Name:       jobRun.GetJobRunID(),
		JobName:    jobRun.GetJobName(),
		Status:     string(prowJob.Status.State),
		StartTime:  prowJob.Status.StartTime.Time,
		EndTime:    endTime,
		ReleaseTag: prowJob.Labels["release.openshift.io/analysis"],
		Cluster:    prowJob.Spec.Cluster,
	}

}

type testRunRow struct {
	prowJob    *prowv1.ProwJob
	jobRun     jobrunaggregatorapi.JobRunInfo
	testSuites []string
	testCase   *junit.TestCase
}

func newTestRunRow(jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob, testSuites []string, testCase *junit.TestCase) *testRunRow {
	return &testRunRow{
		prowJob:    prowJob,
		jobRun:     jobRun,
		testSuites: testSuites,
		testCase:   testCase,
	}

}

var _ bigquery.ValueSaver = &testRunRow{}

func (v *testRunRow) Save() (map[string]bigquery.Value, string, error) {

	// the linter requires not setting a default value. This seems strictly worse and more error-prone to me, but
	// I am a slave to the bot.
	//status := "Unknown"
	var status string
	switch {
	case v.testCase.FailureOutput != nil:
		status = "Failed"
	case v.testCase.SkipMessage != nil:
		status = "Skipped"
	default:
		status = "Passed"
	}

	row := map[string]bigquery.Value{
		"Name":       v.testCase.Name,
		"JobRunName": v.jobRun.GetJobRunID(),
		"JobName":    v.jobRun.GetJobName(),
		"Status":     status,
	}

	return row, "", nil
}
