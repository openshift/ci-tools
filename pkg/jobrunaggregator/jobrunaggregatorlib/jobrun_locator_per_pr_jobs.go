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
	// prowJobReleaseJobNameAnnotation refers to the original periodic job name for PR based payload runs
	prowJobReleaseJobNameAnnotation = "releaseJobName"
)

func NewProwJobMatcherFuncForPR(jobName, matchID, matchLabel string) ProwJobMatcherFunc {
	return perPRProwJobMatcher{
		jobName:    jobName,
		matchID:    matchID,
		matchLabel: matchLabel,
	}.shouldAggregateReleaseControllerJob
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

type perPRProwJobMatcher struct {
	// matchID is how we recognize per-PR payload jobs. It is set based on the matchLabel value
	// that the per-PR payload controller sets in the prowjobs it creates.
	matchID    string
	matchLabel string
	jobName    string
}

func (a perPRProwJobMatcher) shouldAggregateReleaseControllerJob(prowJob *prowjobv1.ProwJob) bool {
	id := prowJob.Labels[a.matchLabel]
	jobName := prowJob.Annotations[prowJobJobNameAnnotation]
	jobRunId := prowJob.Labels[prowJobJobRunIDLabel]
	if releaseJobName, ok := prowJob.Annotations[prowJobReleaseJobNameAnnotation]; ok {
		if releaseJobName != a.jobName {
			return false
		}
	} else {
		return false
	}
	logrus.Infof("  checking %v/%v for matchID match: looking for %q found %q.", jobName, jobRunId, a.matchID, id)
	idMatches := len(a.matchID) > 0 && id == a.matchID

	return idMatches
}
