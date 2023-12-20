package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"path/filepath"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type CIGCSClient interface {
	ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string, logger logrus.FieldLogger) (jobrunaggregatorapi.JobRunInfo, error)
	ReadRelatedJobRuns(ctx context.Context, jobName, gcsPrefix, startingJobRunID, endingJobRunID string,
		matcherFunc ProwJobMatcherFunc) ([]jobrunaggregatorapi.JobRunInfo, error)
}

type ciGCSClient struct {
	gcsClient     *storage.Client
	gcsBucketName string
}

func (o *ciGCSClient) ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string, logger logrus.FieldLogger) (jobrunaggregatorapi.JobRunInfo, error) {
	logger.Debugf("reading job run %s/%s", jobGCSRootLocation, jobRunID)

	bkt := o.gcsClient.Bucket(o.gcsBucketName)
	prowJobPath := fmt.Sprintf("%s/%s/prowjob.json", jobGCSRootLocation, jobRunID)
	jobRunId := filepath.Base(filepath.Dir(prowJobPath))

	jobRun := jobrunaggregatorapi.NewGCSJobRun(bkt, jobGCSRootLocation, jobName, jobRunId, o.gcsBucketName)
	jobRun.SetGCSProwJobPath(prowJobPath)
	_, err := jobRun.GetProwJob(ctx)
	if err != nil {
		logger.WithError(err).Error("failed to get prowjob")
		return nil, fmt.Errorf("failed to get prowjob for %q/%q: %w", jobName, jobRunID, err)
	}

	return jobRun, nil
}

func (o *ciGCSClient) ReadRelatedJobRuns(ctx context.Context,
	jobName, gcsPrefix, startingJobRunID, endingJobRunID string,
	matcherFunc ProwJobMatcherFunc) ([]jobrunaggregatorapi.JobRunInfo, error) {

	logrus.Debugf("searching GCS for related job runs in %s between %s and %s", gcsPrefix, startingJobRunID, endingJobRunID)
	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade/
		Prefix: fmt.Sprintf("%s/", gcsPrefix),

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		// Projection: storage.ProjectionNoACL,
	}

	if startingJobRunID == "" {
		// For debugging, you can set this to a jobID that is not that far away from
		// jobs related to what you are trying to aggregate.
		query.StartOffset = fmt.Sprintf("%s/%s", gcsPrefix, "0")
	} else {
		query.StartOffset = fmt.Sprintf("%s/%s", gcsPrefix, startingJobRunID)
	}
	if endingJobRunID != "" {
		query.EndOffset = fmt.Sprintf("%s/%s", gcsPrefix, endingJobRunID)
	}

	// restrict the query to just one level down
	query.Delimiter = "/"

	fmt.Printf("  starting from %v, ending at %q\n", query.StartOffset, query.EndOffset)

	// Returns an iterator which iterates over the bucket query results.
	// This will list all the folders under the prefix
	bkt := o.gcsClient.Bucket(o.gcsBucketName)
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

		// we are only interested in directories for this pass since we know the file we want
		if len(attrs.Name) > 0 {
			continue
		}

		// we only need prowjob.json at this time
		prowJobPath := fmt.Sprintf("%s%s", attrs.Prefix, "prowjob.json")
		logrus.Debugf("found %s", attrs.Name)
		jobRunId := filepath.Base(filepath.Dir(prowJobPath))
		jobRun := jobrunaggregatorapi.NewGCSJobRun(bkt, gcsPrefix, jobName, jobRunId, o.gcsBucketName)
		jobRun.SetGCSProwJobPath(prowJobPath)

		prowJob, err := jobRun.GetProwJob(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get prowjob for %q/%q: %w", jobName, jobRunId, err)
		}

		if matcherFunc(prowJob) {
			relatedJobRuns = append(relatedJobRuns, jobRun)
		}
	}
	return relatedJobRuns, nil
}
