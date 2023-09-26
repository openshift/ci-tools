package jobrunaggregatorlib

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

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
	FindRelatedJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error)
	FindJob(ctx context.Context, jobRunID string) (jobrunaggregatorapi.JobRunInfo, error)
}

// ProwJobMatcherFunc defines a function signature for matching prow jobs. The function is
// used by different analyzers and lower layers to match jobs for relevant tasks.
// - for payload based aggregator, it matches with payload tags
// - for PR based aggregator, it matches with aggregation id or payload invocation id.
// - for test case analyzer, there are two levels of matching: one for matching jobs (based on names etc)
// while the other for matching job runs. The mechanism for matching job runs uses the above payload or PR
// based aggregator matching functions
//
// It is kept this way to keep changes to the minimum.
type ProwJobMatcherFunc func(prowJob *prowjobv1.ProwJob) bool

type analysisJobAggregator struct {
	jobName string

	prowJobMatcher ProwJobMatcherFunc
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
	prowJobMatcher ProwJobMatcherFunc,
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

// FindRelatedJobs returns a slice of JobRunInfo which has info contained in GCS buckets
// used to determine pass/fail.
func (a *analysisJobAggregator) FindRelatedJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
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

	return a.ciGCSClient.ReadRelatedJobRuns(ctx, a.jobName, a.gcsPrefix, startingJobRunID, endingJobRunID, a.prowJobMatcher)
}

func (a *analysisJobAggregator) FindJob(ctx context.Context, jobRunID string) (jobrunaggregatorapi.JobRunInfo, error) {
	return a.ciGCSClient.ReadJobRunFromGCS(ctx, a.gcsPrefix, a.jobName, jobRunID, logrus.New())
}
