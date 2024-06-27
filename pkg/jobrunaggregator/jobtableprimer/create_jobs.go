package jobtableprimer

import (
	"context"
	"fmt"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

const (
	minMajor = 4
	minMinor = 15
)

type CreateJobsOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient

	jobInserter jobrunaggregatorlib.BigQueryInserter
	gcsBucket   string
}

// getNewReleases get a list of releases defined in the Releases table in BigQuery. It then filters
// out older releases whose jobs we do not care about importing.
func (o *CreateJobsOptions) getNewReleases(ctx context.Context, ciDataClient jobrunaggregatorlib.CIDataClient) ([]jobrunaggregatorapi.ReleaseRow, error) {
	newReleases := []jobrunaggregatorapi.ReleaseRow{}
	releases, err := ciDataClient.ListReleases(ctx)
	if err != nil {
		return newReleases, err
	}
	for _, release := range releases {
		if release.Major > minMajor ||
			(release.Major == minMajor && release.Minor >= minMinor) {
			newReleases = append(newReleases, release)
		}
	}
	return newReleases, nil
}

func (o *CreateJobsOptions) createJobRowsFromReleases(ctx context.Context, ciDataClient jobrunaggregatorlib.CIDataClient) ([]jobrunaggregatorapi.JobRow, error) {
	releases, err := o.getNewReleases(ctx, ciDataClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get releases: %w", err)
	}

	// Update source URLs including periodic and release-controller URLs
	jobNameGenerator := newJobNameGenerator()
	jobNameGenerator.UpdateURLsForAllReleases(releases)
	jobNames, err := jobNameGenerator.GenerateJobNames()
	if err != nil {
		return nil, err
	}

	// Create job rows
	jobRowListBuilder := newJobRowListBuilder(releases)
	jobRowsToCreate := jobRowListBuilder.CreateAllJobRows(jobNames)

	return jobRowsToCreate, nil
}

func (o *CreateJobsOptions) Run(ctx context.Context) error {
	fmt.Printf("Creating jobs from releases\n")
	jobsToCreate, err := o.createJobRowsFromReleases(ctx, o.ciDataClient)
	if err != nil {
		return fmt.Errorf("failed to create job rows from releases: %w", err)
	}

	fmt.Printf("Priming jobs\n")

	existingJobs, err := o.ciDataClient.ListAllJobsWithVariants(ctx)
	if err != nil {
		return fmt.Errorf("failed to get jobs: %w", err)
	}

	missingJobs := []jobrunaggregatorapi.JobRow{}
	for i := range jobsToCreate {
		jobToCreate := jobsToCreate[i]
		alreadyExists := false
		for _, existing := range existingJobs {
			if existing.JobName == jobToCreate.JobName {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		jobToCreate.GCSBucketName = o.gcsBucket
		missingJobs = append(missingJobs, jobToCreate)
	}

	fmt.Printf("Inserting %d jobs\n", len(missingJobs))
	if err := o.jobInserter.Put(ctx, missingJobs); err != nil {
		return err
	}

	return nil
}
