package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type disruptionUploader struct {
	backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter
}

func newDisruptionUploader(backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter) uploader {
	return &disruptionUploader{
		backendDisruptionInserter: backendDisruptionInserter,
	}
}

func (o *disruptionUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) error {
	fmt.Printf("  uploading backend disruption results: %q/%q\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	backendDisruptionData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "backend-disruption")
	if err != nil {
		return err
	}
	if len(backendDisruptionData) > 0 {
		return o.uploadBackendDisruptionFromDirectData(ctx, jobRun.GetJobRunID(), backendDisruptionData)
	}

	dateWeStartedTrackingDirectDisruptionData, err := time.Parse(time.RFC3339, "2021-11-08T00:00:00Z")
	if err != nil {
		return err
	}
	// TODO fix better before we hit 4.20
	releaseHasDisruptionData := strings.Contains(jobRun.GetJobName(), "4.10") ||
		strings.Contains(jobRun.GetJobName(), "4.11") ||
		strings.Contains(jobRun.GetJobName(), "4.12") ||
		strings.Contains(jobRun.GetJobName(), "4.13") ||
		strings.Contains(jobRun.GetJobName(), "4.14") ||
		strings.Contains(jobRun.GetJobName(), "4.15") ||
		strings.Contains(jobRun.GetJobName(), "4.16") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.19")
	if releaseHasDisruptionData && prowJob.CreationTimestamp.After(dateWeStartedTrackingDirectDisruptionData) {
		fmt.Printf("  No disruption data found, returning: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
		// we  have no data, just return
		return nil
	}

	fmt.Printf("  missing direct backend disruption results, trying to read from junit: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	// if we don't have
	combinedJunitContent, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return err
	}

	return o.uploadBackendDisruptionFromJunit(ctx, jobRun.GetJobRunID(), combinedJunitContent)
}

func (o *disruptionUploader) uploadBackendDisruptionFromJunit(ctx context.Context, jobRunName string, suites *junit.TestSuites) error {
	serverAvailabilityResults := jobrunaggregatorlib.GetServerAvailabilityResultsFromJunit(suites)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}

func (o *disruptionUploader) uploadBackendDisruptionFromDirectData(ctx context.Context, jobRunName string, backendDisruptionData map[string]string) error {
	serverAvailabilityResults := jobrunaggregatorlib.GetServerAvailabilityResultsFromDirectData(backendDisruptionData)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}
func (o *disruptionUploader) uploadBackendDisruption(ctx context.Context, jobRunName string, serverAvailabilityResults map[string]jobrunaggregatorlib.AvailabilityResult) error {
	rows := []*jobrunaggregatorapi.BackendDisruptionRow{}
	for _, backendName := range sets.StringKeySet(serverAvailabilityResults).List() {
		unavailability := serverAvailabilityResults[backendName]
		row := &jobrunaggregatorapi.BackendDisruptionRow{
			BackendName:       backendName,
			JobRunName:        jobRunName,
			DisruptionSeconds: unavailability.SecondsUnavailable,
		}
		rows = append(rows, row)
	}
	if err := o.backendDisruptionInserter.Put(ctx, rows); err != nil {
		return err
	}
	return nil
}
