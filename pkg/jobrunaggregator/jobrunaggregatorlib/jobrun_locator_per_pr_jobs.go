package jobrunaggregatorlib

import (
	"fmt"
	"time"

	"cloud.google.com/go/storage"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func GetAggregationIDFromProwJob(prowJob *prowjobv1.ProwJob) string {
	return prowJob.Labels["release.openshift.io/aggregation-id"]
}

func NewPayloadAnalysisJobLocatorForPR(
	jobName, aggregationID string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsClient *storage.Client,
	gcsBucketName string,
	gcsPrefix string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		perPRProwJobMatcher{
			aggregationID: aggregationID,
		}.shouldAggregateReleaseControllerJob,
		startTime,
		ciDataClient,
		ciGCSClient,
		gcsClient,
		gcsBucketName,
		gcsPrefix,
	)
}

type perPRProwJobMatcher struct {
	// aggregationID is how we recognize repeated per-PR jobs.  It is set when invoking the aggregator based on the value
	// that the per-PR payload controller sets in the prowjobs it creates.
	aggregationID string
}

func (a perPRProwJobMatcher) shouldAggregateReleaseControllerJob(prowJob *prowjobv1.ProwJob) bool {
	aggregationID := GetAggregationIDFromProwJob(prowJob)
	jobName := prowJob.Labels["prow.k8s.io/job"]
	jobRunId := prowJob.Labels["prow.k8s.io/build-id"]
	fmt.Printf("  checking %v/%v for aggregationID match: looking for %q found %q.\n", jobName, jobRunId, a.aggregationID, aggregationID)
	aggregationIDMatches := len(a.aggregationID) > 0 && aggregationID == a.aggregationID

	return aggregationIDMatches
}
