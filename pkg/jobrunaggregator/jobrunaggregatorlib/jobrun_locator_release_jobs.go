package jobrunaggregatorlib

import (
	"fmt"
	"time"

	"cloud.google.com/go/storage"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func GetPayloadTagFromProwJob(prowJob *prowjobv1.ProwJob) string {
	return prowJob.Labels["release.openshift.io/analysis"]
}

func NewPayloadAnalysisJobLocatorForReleaseController(
	jobName, payloadTag string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsClient *storage.Client,
	gcsBucketName string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		releaseControllerProwJobMatcher{
			payloadTag: payloadTag,
		}.shouldAggregateReleaseControllerJob,
		startTime,
		ciDataClient,
		ciGCSClient,
		gcsClient,
		gcsBucketName,
		"logs/"+jobName,
	)
}

type releaseControllerProwJobMatcher struct {
	payloadTag string
}

func (a releaseControllerProwJobMatcher) shouldAggregateReleaseControllerJob(prowJob *prowjobv1.ProwJob) bool {
	payloadTag := GetPayloadTagFromProwJob(prowJob)
	jobName := prowJob.Labels["prow.k8s.io/job"]
	jobRunId := prowJob.Labels["prow.k8s.io/build-id"]
	fmt.Printf("  checking %v/%v for payloadtag match: looking for %q found %q.\n", jobName, jobRunId, a.payloadTag, payloadTag)
	payloadTagMatches := len(a.payloadTag) > 0 && payloadTag == a.payloadTag
	if payloadTagMatches {
		return true
	}

	return false
}
