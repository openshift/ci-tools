package jobrunaggregatorlib

import (
	"time"

	"github.com/sirupsen/logrus"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const (
	// ProwJobAggregationIDLabel is the name of the label for the aggregation id in prow job
	ProwJobAggregationIDLabel = "release.openshift.io/aggregation-id"
	// ProwJobPayloadInvocationIDLabel is the name of the label for the payload invocation id in prow job
	ProwJobPayloadInvocationIDLabel = "release.openshift.io/aggregation-id"
	// prowJobReleaseJobNameAnnotation refers to the original periodic job name for PR based payload runs.
	// This is a special case for the PR invoked payload jobs where ProwJobJobNameAnnotation annotation
	// refers to a uniquely generated name per job run. Thus, prowJobReleaseJobNameAnnotation is used to
	// refer to the original job name.
	prowJobReleaseJobNameAnnotation = "releaseJobName"
)

func NewProwJobMatcherFuncForPR(matchJobName, matchID, matchLabel string) ProwJobMatcherFunc {
	return func(prowJob *prowjobv1.ProwJob) bool {
		id := prowJob.Labels[matchLabel]
		jobName := prowJob.Annotations[ProwJobJobNameAnnotation]
		jobRunId := prowJob.Labels[prowJobJobRunIDLabel]
		if releaseJobName, ok := prowJob.Annotations[prowJobReleaseJobNameAnnotation]; ok {
			if releaseJobName != matchJobName {
				return false
			}
		} else {
			return false
		}
		logrus.Infof("  checking %v/%v for matchID match: looking for %q found %q.", jobName, jobRunId, matchID, id)
		idMatches := len(matchID) > 0 && id == matchID

		return idMatches
	}
}

func NewPayloadAnalysisJobLocatorForPR(
	jobName, matchID, matchLabel string,
	startTime time.Time,
	ciDataClient AggregationJobClient,
	ciGCSClient CIGCSClient,
	gcsBucketName string,
	gcsPrefix string) JobRunLocator {

	return NewPayloadAnalysisJobLocator(
		jobName,
		NewProwJobMatcherFuncForPR(jobName, matchID, matchLabel),
		startTime,
		ciDataClient,
		ciGCSClient,
		gcsBucketName,
		gcsPrefix,
	)
}
