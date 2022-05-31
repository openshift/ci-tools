package jobrunaggregatorlib

import (
	"fmt"
	"time"

	"cloud.google.com/go/storage"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	// AggregationIDLabel is the name of the label for the aggregation id in prow job
	AggregationIDLabel = "release.openshift.io/aggregation-id"
	// PayloadInvocationIDLabel is the name of the label for the payload invocation id in prow job
	PayloadInvocationIDLabel = "release.openshift.io/aggregation-id"
)

func NewPayloadAnalysisJobLocatorForPR(
	jobName, matchID, matchLabel string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsClient *storage.Client,
	gcsBucketName string,
	gcsPrefix string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		perPRProwJobMatcher{
			matchID:    matchID,
			matchLabel: matchLabel,
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
	// matchID is how we recognize per-PR payload jobs. It is set based on the matchLabel value
	// that the per-PR payload controller sets in the prowjobs it creates.
	matchID    string
	matchLabel string
}

func (a perPRProwJobMatcher) shouldAggregateReleaseControllerJob(prowJob *prowjobv1.ProwJob) bool {
	id := prowJob.Labels[a.matchLabel]
	jobName := prowJob.Labels["prow.k8s.io/job"]
	jobRunId := prowJob.Labels["prow.k8s.io/build-id"]
	fmt.Printf("  checking %v/%v for matchID match: looking for %q found %q.\n", jobName, jobRunId, a.matchID, id)
	idMatches := len(a.matchID) > 0 && id == a.matchID

	return idMatches
}
