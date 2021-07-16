package jobrunaggregator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
	"k8s.io/apimachinery/pkg/util/yaml"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	utiltrace "k8s.io/utils/trace"
)

type JobRun struct {
	ProwJob   *prowjobv1.ProwJob
	GCSBucket string
}

func (o *JobRunAggregatorOptions) ReadProwJob(ctx context.Context) ([]JobRun, error) {
	fmt.Printf("Reading prowjobs for job %v.\n", o.JobName)

	prowJobPaths, err := o.getProwJobPathsForJob(ctx)
	if err != nil {
		return nil, err
	}

	jobRuns := []JobRun{}
	bkt := o.GCSClient.Bucket(openshiftCIBucket)
	// Iterate through the ProwJob paths, retrieve the objects and decode them into a struct for further processing
	for _, prowJobPath := range prowJobPaths {
		prowJob, err := getProwJob(ctx, bkt, prowJobPath)
		if err != nil {
			return nil, err
		}

		logrus.Infof("Decoded %s into struct", prowJobPath)

		jobRuns = append(jobRuns, JobRun{
			ProwJob:   prowJob,
			GCSBucket: prowJobPath,
		})
	}

	return jobRuns, nil
}

func (o *JobRunAggregatorOptions) traceFields() []utiltrace.Field {
	return []utiltrace.Field{
		{Key: "jobName", Value: o.JobName},
	}
}

func (o *JobRunAggregatorOptions) getProwJobPathsForJob(ctx context.Context) ([]string, error) {
	trace := utiltrace.New("GetProwJobs", o.traceFields()...)
	defer trace.LogIfLong(500 * time.Millisecond)

	prowJobJsonFiles := []string{}

	bkt := o.GCSClient.Bucket(openshiftCIBucket)

	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade
		Prefix: "logs/" + o.JobName,

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		//Projection: storage.ProjectionNoACL,
	}

	// Only retrieve the name and creation time for performance
	if err := query.SetAttrSelection([]string{"Name", "Created"}); err != nil {
		return nil, err
	}
	// TODO need to discover this based on our current cache.
	query.StartOffset = "logs/periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade/1414926164144689152"
	trace.Step("Query configured.")

	now := time.Now()

	// Returns an iterator which iterates over the bucket query results.
	// Unfortunately, this will list *all* files with the query prefix.
	it := bkt.Objects(ctx, query)
	trace.Step("Iterator retrieved.")

	// Find the query results we're the most interested in. In this case, we're
	// interested in files called prowjob.json that were created less than 24
	// hours ago.
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return prowJobJsonFiles, err
		}

		if strings.HasSuffix(attrs.Name, "prowjob.json") && now.Sub(attrs.Created) < (24*time.Hour) {
			logrus.Infof("Found %s", attrs.Name)
			prowJobJsonFiles = append(prowJobJsonFiles, attrs.Name)
		}
	}
	trace.Step("List filtered.")

	return prowJobJsonFiles, nil
}

func getProwJob(ctx context.Context, bkt *storage.BucketHandle, prowJobPath string) (*prowjobv1.ProwJob, error) {
	prowJob := &prowjobv1.ProwJob{}

	// Get an Object handle for the path
	obj := bkt.Object(prowJobPath)

	// Get an io.Reader for the object.
	gcsReader, err := obj.NewReader(ctx)
	if err != nil {
		return prowJob, err
	}

	defer gcsReader.Close()

	// Decode it into a struct. Using the api-machinery package might be overkill here though.
	err = yaml.NewYAMLOrJSONDecoder(gcsReader, 4096).Decode(&prowJob)

	return prowJob, err
}
