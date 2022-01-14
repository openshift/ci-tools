package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type CIGCSClient interface {
	ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string) (jobrunaggregatorapi.JobRunInfo, error)

	// ListJobRunNames returns a string channel for jobRunNames, an error channel for reporting errors during listing,
	// and an error if the listing cannot begin.
	ListJobRunNamesOlderThanFourHours(ctx context.Context, jobName, startingID string) (chan string, chan error, error)
}

type ciGCSClient struct {
	gcsClient     *storage.Client
	gcsBucketName string
}

func (o *ciGCSClient) ListJobRunNamesOlderThanFourHours(ctx context.Context, jobName, startingID string) (chan string, chan error, error) {
	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade
		Prefix: "logs/" + jobName,

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		//Projection: storage.ProjectionNoACL,
	}

	// Only retrieve the name and creation time for performance
	if err := query.SetAttrSelection([]string{"Name", "Created"}); err != nil {
		return nil, nil, err
	}

	// When debugging, you can set the starting ID to a number such that you
	// will process a relatively small number of jobsRuns.
	query.StartOffset = fmt.Sprintf("logs/%s/%s", jobName, startingID)
	fmt.Printf("  starting from %v\n", query.StartOffset)

	now := time.Now()

	// Returns an iterator which iterates over the bucket query results.
	// Unfortunately, this will list *all* files with the query prefix.
	bkt := o.gcsClient.Bucket(o.gcsBucketName)
	it := bkt.Objects(ctx, query)

	errorCh := make(chan error, 100)
	jobRunProcessingCh := make(chan string, 100)
	// Find the query results we're the most interested in. In this case, we're interested in files called prowjob.json
	// so that we only get each jobrun once and we queue them in a channel
	go func() {
		defer close(jobRunProcessingCh)

		for {
			if ctx.Err() != nil {
				return
			}

			attrs, err := it.Next()
			if err == iterator.Done {
				// we're done adding values, so close the channel
				return
			}
			if err != nil {
				errorCh <- err
				return
			}

			// TODO if it's more than 100 days old, we don't need it
			if now.Sub(attrs.Created) > (100 * 24 * time.Hour) {
				continue
			}
			// chosen because CI jobs only take four hours max (so far), so we only get completed jobs
			if now.Sub(attrs.Created) < (4 * time.Hour) {
				continue
			}

			switch {
			case strings.HasSuffix(attrs.Name, "prowjob.json"):
				jobRunId := filepath.Base(filepath.Dir(attrs.Name))
				fmt.Printf("Queued jobrun/%q/%q\n", jobName, jobRunId)
				jobRunProcessingCh <- jobRunId

			default:
				//fmt.Printf("checking %q\n", attrs.Name)
			}
		}
	}()

	return jobRunProcessingCh, errorCh, nil
}

func (o *ciGCSClient) ReadJobRunFromGCS(ctx context.Context, jobGCSRootLocation, jobName, jobRunID string) (jobrunaggregatorapi.JobRunInfo, error) {
	fmt.Printf("reading job run %v/%v.\n", jobGCSRootLocation, jobRunID)

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
			fmt.Printf("  found %s\n", attrs.Name)
			jobRunId := filepath.Base(filepath.Dir(attrs.Name))
			if jobRun == nil {
				jobRun = jobrunaggregatorapi.NewGCSJobRun(bkt, jobGCSRootLocation, jobName, jobRunId)
			}
			jobRun.SetGCSProwJobPath(attrs.Name)

		case strings.HasSuffix(attrs.Name, ".xml") && strings.Contains(attrs.Name, "/junit"):
			fmt.Printf("  found %s\n", attrs.Name)
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
		fmt.Printf("  removing %q/%q because it doesn't have a prowjob.json\n", jobName, jobRunID)
		return nil, nil
	}
	if len(jobRun.GetGCSProwJobPath()) == 0 {
		fmt.Printf("  removing %q/%q because it doesn't have a prowjob.json but does have junit\n", jobName, jobRunID)
		return nil, nil
	}
	_, err := jobRun.GetProwJob(ctx)
	if err != nil {
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
