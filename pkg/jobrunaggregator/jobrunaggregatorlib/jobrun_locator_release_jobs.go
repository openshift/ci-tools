package jobrunaggregatorlib

import (
	"time"

	"github.com/sirupsen/logrus"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

// GetPayloadTagFromProwJob gets the payload tag from prow jobs.
func GetPayloadTagFromProwJob(prowJob *prowjobv1.ProwJob) string {
	return prowJob.Annotations["release.openshift.io/tag"]
}

func NewPayloadAnalysisJobLocatorForReleaseController(
	jobName, payloadTag string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsBucketName string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		releaseControllerProwJobMatcher{
			payloadTag: payloadTag,
		}.shouldAggregateReleaseControllerJob,
		startTime,
		ciDataClient,
		ciGCSClient,
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
	logrus.Infof("checking %v/%v for payloadtag match: looking for %q found %q", jobName, jobRunId, a.payloadTag, payloadTag)
	payloadTagMatches := len(a.payloadTag) > 0 && payloadTag == a.payloadTag

	return payloadTagMatches
}
