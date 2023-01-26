package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type CIGCSClient interface {
	ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string, logger logrus.FieldLogger) (jobrunaggregatorapi.JobRunInfo, error)
}

type ciGCSClient struct {
	gcsClient     *storage.Client
	gcsBucketName string
}

func (o *ciGCSClient) ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string, logger logrus.FieldLogger) (jobrunaggregatorapi.JobRunInfo, error) {
	logger.Debugf("reading job run %s/%s", jobGCSRootLocation, jobRunID)

	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade
		Prefix: jobGCSRootLocation,

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		//Projection: storage.ProjectionNoACL,
	}

	// Only retrieve the name and creation time for performance
	if err := query.SetAttrSelection([]string{"Name", "Created", "Generation"}); err != nil {
		return nil, err
	}
	// start reading for this jobrun bucket
	query.StartOffset = fmt.Sprintf("%s/%s", jobGCSRootLocation, jobRunID)
	// end reading after this jobrun bucket
	query.EndOffset = fmt.Sprintf("%s/%s", jobGCSRootLocation, NextJobRunID(jobRunID))

	// Returns an iterator which iterates over the bucket query results.
	// Unfortunately, this will list *all* files with the query prefix.
	bkt := o.gcsClient.Bucket(o.gcsBucketName)
	it := bkt.Objects(ctx, query)

	// Find the query results we're the most interested in. In this case, we're
	// interested in files called prowjob.json that were created less than 24
	// hours ago.
	var jobRun jobrunaggregatorapi.JobRunInfo
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		switch {
		case strings.HasSuffix(attrs.Name, "prowjob.json"):
			logger.Debugf("found %s", attrs.Name)
			jobRunId := filepath.Base(filepath.Dir(attrs.Name))
			if jobRun == nil {
				jobRun = jobrunaggregatorapi.NewGCSJobRun(bkt, jobGCSRootLocation, jobName, jobRunId)
			}
			jobRun.SetGCSProwJobPath(attrs.Name)

		case strings.HasSuffix(attrs.Name, ".xml") && strings.Contains(attrs.Name, "/junit"):
			logger.Debugf("found %s", attrs.Name)
			nameParts := strings.Split(attrs.Name, "/")
			if len(nameParts) < 4 {
				continue
			}
			jobRunId := nameParts[2]
			if jobRun == nil {
				jobRun = jobrunaggregatorapi.NewGCSJobRun(bkt, jobGCSRootLocation, jobName, jobRunId)
			}
			jobRun.AddGCSJunitPaths(attrs.Name)

		default:
			//fmt.Printf("checking %q\n", attrs.Name)
		}
	}

	// eliminate items without prowjob.json and ones that aren't finished
	if jobRun == nil {
		logger.Info("removing job run because it doesn't have a prowjob.json")
		return nil, nil
	}
	if len(jobRun.GetGCSProwJobPath()) == 0 {
		logger.Info("removing job run because it doesn't have a prowjob.json but does have junit")
		return nil, nil
	}
	_, err := jobRun.GetProwJob(ctx)
	if err != nil {
		logger.WithError(err).Error("failed to get prowjob")
		return nil, fmt.Errorf("failed to get prowjob for %q/%q: %w", jobName, jobRunID, err)
	}

	return jobRun, nil
}

func NextJobRunID(curr string) string {
	if len(curr) == 0 {
		return "0"
	}
	idAsInt, err := strconv.ParseInt(curr, 10, 64)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%d", idAsInt+1)
}
