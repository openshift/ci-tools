package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"cloud.google.com/go/storage"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"google.golang.org/api/iterator"
)

func GetPayloadTagFromProwJob(prowJob *prowjobv1.ProwJob) string {
	return prowJob.Labels["release.openshift.io/analysis"]
}

type JobRunLocator interface {
	FindRelatedJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error)
}

type analysisJobAggregator struct {
	jobName string

	// payloadTag is how we will recognize repeated jobs
	payloadTag string
	// startTime is the time when the analysis jobs were started.  We'll look plus or minus a day from here to bound the
	// bigquery dataset.
	startTime time.Time

	ciDataClient  CIDataClient
	ciGCSClient   CIGCSClient
	gcsClient     *storage.Client
	gcsBucketName string
}

func NewPayloadAnalysisJobLocator(
	jobName, payloadTag string,
	startTime time.Time,
	ciDataClient CIDataClient,
	ciGCSClient CIGCSClient,
	gcsClient *storage.Client,
	gcsBucketName string) JobRunLocator {

	return &analysisJobAggregator{
		jobName:       jobName,
		payloadTag:    payloadTag,
		startTime:     startTime,
		ciDataClient:  ciDataClient,
		ciGCSClient:   ciGCSClient,
		gcsClient:     gcsClient,
		gcsBucketName: gcsBucketName,
	}
}

func (a *analysisJobAggregator) FindRelatedJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
	startOfJobRunWindow := a.startTime.Add(-1 * 1 * time.Hour)
	endOfJobRunWindow := a.startTime.Add(1 * 4 * time.Hour)
	startingJobRun, err := a.ciDataClient.GetJobRunForJobNameBeforeTime(ctx, a.jobName, startOfJobRunWindow)
	if err != nil {
		return nil, err
	}
	endingJobRun, err := a.ciDataClient.GetJobRunForJobNameAfterTime(ctx, a.jobName, endOfJobRunWindow)
	if err != nil {
		return nil, err
	}

	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade
		Prefix: "logs/" + a.jobName,

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		//Projection: storage.ProjectionNoACL,
	}

	// Only retrieve the name and creation time for performance
	if err := query.SetAttrSelection([]string{"Name", "Created"}); err != nil {
		return nil, err
	}
	if startingJobRun == nil {
		query.StartOffset = fmt.Sprintf("logs/%s/%s", a.jobName, "0")
	} else {
		query.StartOffset = fmt.Sprintf("logs/%s/%s", a.jobName, startingJobRun.Name)
	}
	if false && endingJobRun != nil {
		query.EndOffset = fmt.Sprintf("logs/%s/%s", a.jobName, endingJobRun.Name)
	}
	fmt.Printf("  starting from %v, ending at %q\n", query.StartOffset, query.EndOffset)

	// Returns an iterator which iterates over the bucket query results.
	// Unfortunately, this will list *all* files with the query prefix.
	bkt := a.gcsClient.Bucket(a.gcsBucketName)
	it := bkt.Objects(ctx, query)

	// Find the query results we're the most interested in. In this case, we're interested in files called prowjob.json
	// so that we only get each jobrun once and we queue them in a channel
	relatedJobRuns := []jobrunaggregatorapi.JobRunInfo{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			// we're done adding values, so close the channel
			break
		}
		if err != nil {
			return nil, err
		}

		switch {
		case strings.HasSuffix(attrs.Name, "prowjob.json"):
			jobRunId := filepath.Base(filepath.Dir(attrs.Name))
			jobRunInfo, err := a.ciGCSClient.ReadJobRunFromGCS(ctx, a.jobName, jobRunId)
			if err != nil {
				return nil, err
			}
			prowJob, err := jobRunInfo.GetProwJob(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get prowjob for %q/%q: %w", a.jobName, jobRunId, err)
			}

			if _, ok := prowJob.Labels["release.openshift.io/aggregator"]; ok {
				fmt.Printf("  skipping the aggregator prowjob %q/%q\n", a.jobName, jobRunId)
				continue
			}

			payloadTag := GetPayloadTagFromProwJob(prowJob)
			fmt.Printf("  checking %v/%v for payloadtag match: looking for %q found %q.\n", a.jobName, jobRunId, a.payloadTag, payloadTag)
			if payloadTag == a.payloadTag {
				relatedJobRuns = append(relatedJobRuns, jobRunInfo)
			}

		default:
			//fmt.Printf("checking %q\n", attrs.Name)
		}
	}

	return relatedJobRuns, nil
}
