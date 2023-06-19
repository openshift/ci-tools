package jobrunaggregatorlib

import (
	"context"
	"time"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

var (
	// JobSearchWindowStartOffset defines the start offset of the job search window.
	JobSearchWindowStartOffset time.Duration = 1 * time.Hour
	// JobSearchWindowEndOffset defines the end offset of the job search window.
	JobSearchWindowEndOffset time.Duration = 4 * time.Hour
)

type JobRunLocator interface {
	FindRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error)
}

type prowJobMatcherFunc func(prowJob *prowjobv1.ProwJob) bool

type analysisJobAggregator struct {
	jobName string

	prowJobMatcher prowJobMatcherFunc
	// startTime is the time when the analysis jobs were started.  We'll look plus or minus a day from here to bound the
	// bigquery dataset.
	startTime time.Time

	ciDataClient  AggregationJobClient
	ciGCSClient   CIGCSClient
	gcsBucketName string
	gcsPrefix     string
}

func NewPayloadAnalysisJobLocator(
	jobName string,
	prowJobMatcher prowJobMatcherFunc,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsBucketName string,
	gcsPrefix string) JobRunLocator {

	return &analysisJobAggregator{
		jobName:        jobName,
		prowJobMatcher: prowJobMatcher,
		startTime:      startTime,
		ciDataClient:   ciDataClient,
		ciGCSClient:    ciGCSClient,
		gcsBucketName:  gcsBucketName,
		gcsPrefix:      gcsPrefix,
	}
}

// FindRelatedJobRuns returns a slice of JobRunInfo which has info contained in GCS buckets
// used to determine pass/fail.
func (a *analysisJobAggregator) FindRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
	startOfJobRunWindow := a.startTime.Add(-1 * JobSearchWindowStartOffset)
	endOfJobRunWindow := a.startTime.Add(JobSearchWindowEndOffset)
	startingJobRunID, err := a.ciDataClient.GetJobRunForJobNameBeforeTime(ctx, a.jobName, startOfJobRunWindow)
	if err != nil {
		return nil, err
	}
	endingJobRunID, err := a.ciDataClient.GetJobRunForJobNameAfterTime(ctx, a.jobName, endOfJobRunWindow)
	if err != nil {
		return nil, err
	}

	relatedJobRuns, err := a.ciGCSClient.ReadRelatedJobRuns(ctx, a.jobName, a.gcsPrefix, startingJobRunID, endingJobRunID, a.prowJobMatcher)

	return relatedJobRuns, nil
}
