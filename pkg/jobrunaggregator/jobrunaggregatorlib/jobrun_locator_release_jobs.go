package jobrunaggregatorlib

import (
	"time"

	"github.com/sirupsen/logrus"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	// ProwJobPayloadTagAnnotation is the name of the annotation for the payload tag in prow job
	ProwJobPayloadTagAnnotation = "release.openshift.io/tag"
)

// GetPayloadTagFromProwJob gets the payload tag from prow jobs.
func GetPayloadTagFromProwJob(prowJob *prowjobv1.ProwJob) string {
	return prowJob.Annotations[ProwJobPayloadTagAnnotation]
}

func NewProwJobMatcherFuncForReleaseController(matchJobName, matchPayloadTag string) ProwJobMatcherFunc {
	return func(prowJob *prowjobv1.ProwJob) bool {
		payloadTag := GetPayloadTagFromProwJob(prowJob)
		jobName := prowJob.Annotations[ProwJobJobNameAnnotation]
		jobRunId := prowJob.Labels[prowJobJobRunIDLabel]
		if jobName != matchJobName {
			return false
		}
		logrus.Infof("checking %v/%v for payloadtag match: looking for %q found %q", jobName, jobRunId, matchPayloadTag, payloadTag)
		payloadTagMatches := len(matchPayloadTag) > 0 && payloadTag == matchPayloadTag

		return payloadTagMatches
	}
}

func NewPayloadAnalysisJobLocatorForReleaseController(
	jobName, payloadTag string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsBucketName string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		NewProwJobMatcherFuncForReleaseController(jobName, payloadTag),
		startTime,
		ciDataClient,
		ciGCSClient,
		gcsBucketName,
		"logs/"+jobName,
	)
}
