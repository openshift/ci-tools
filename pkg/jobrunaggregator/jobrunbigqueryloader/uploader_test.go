package jobrunbigqueryloader

import (
	"context"
	"testing"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func TestJobRunBigQueryLoaderOptions_uploadTestSuites(t *testing.T) {
	type fields struct {
		JobName    string
		WorkingDir string
		PayloadTag string

		JobRunInserter  BigQueryInserter
		TestRunInserter BigQueryInserter
	}
	type args struct {
		ctx     context.Context
		jobRun  jobrunaggregatorapi.JobRunInfo
		prowJob *prowv1.ProwJob
		suites  *junit.TestSuites
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &jobLoaderOptions{
				jobName:         tt.fields.JobName,
				WorkingDir:      tt.fields.WorkingDir,
				PayloadTag:      tt.fields.PayloadTag,
				jobRunInserter:  tt.fields.JobRunInserter,
				testRunInserter: tt.fields.TestRunInserter,
			}
			if err := o.uploadTestSuites(tt.args.ctx, tt.args.jobRun, tt.args.prowJob, tt.args.suites); (err != nil) != tt.wantErr {
				t.Errorf("uploadTestSuites() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
