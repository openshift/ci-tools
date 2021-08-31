package jobrunaggregatorcachebuilder

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"cloud.google.com/go/storage"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

// JobRunsAggregatorCacheBuilderOptions reads prowjob.json and junit files for the specified job and caches them
// to the local disk for use by other processes.
type JobRunsAggregatorCacheBuilderOptions struct {
	JobNames   []string
	GCSClient  *storage.Client
	WorkingDir string
}

func (o *JobRunsAggregatorCacheBuilderOptions) Run(ctx context.Context) error {
	fmt.Printf("Launching threads to cache job runs\n")

	waitGroup := sync.WaitGroup{}
	errCh := make(chan error, len(o.JobNames))
	for i := range o.JobNames {
		jobName := o.JobNames[i]
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			currOptions := JobRunAggregatorCacheBuilderOptions{
				JobName:    jobName,
				GCSClient:  o.GCSClient,
				WorkingDir: o.WorkingDir,
			}
			err := currOptions.Run(ctx)
			if err != nil {
				errCh <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// JobRunAggregatorCacheBuilderOptions reads prowjob.json and junit files for the specified job and caches them
// to the local disk for use by other processes.
type JobRunAggregatorCacheBuilderOptions struct {
	JobName    string
	GCSClient  *storage.Client
	WorkingDir string
}

func (o *JobRunAggregatorCacheBuilderOptions) Run(ctx context.Context) error {
	fmt.Printf("Caching job runs of type %v.\n", o.JobName)

	// find the biggest local run
	startFromRunID := int64(0)
	jobDir := filepath.Join(o.WorkingDir, "logs", o.JobName)
	jobRunDirs, err := ioutil.ReadDir(jobDir)
	switch {
	case os.IsNotExist(err):
	// do nothing
	case err != nil:
		return err
	default:
		for _, currJobRunDir := range jobRunDirs {
			dirName := filepath.Base(currJobRunDir.Name())
			biggestValue, err := strconv.ParseInt(dirName, 10, 64)
			if err != nil {
				continue
			}
			if biggestValue > startFromRunID {
				startFromRunID = biggestValue + 1 // always start from the next one
			}
		}
	}
	fmt.Printf("Starting job: %q from %d\n", o.JobName, startFromRunID)

	jobRuns, err := o.ReadProwJobs(ctx, startFromRunID)
	if err != nil {
		return err
	}

	for _, jobRun := range jobRuns {
		// Iterate through the ProwJob paths, retrieve the objects and decode them into a struct for further processing
		// call made to fill the content
		if _, err := jobRun.GetAllContent(ctx); err != nil {
			return err
		}
		fmt.Printf("retrieved all content for job: %q,  run: %q\n", jobRun.GetJobName(), jobRun.GetJobRunID())

		prowJob, err := jobRun.GetProwJob(ctx)
		if err != nil {
			return err
		}
		if prowJob.Status.CompletionTime == nil {
			continue
		}

		// to match GCS bucket
		if err := jobRun.WriteCache(ctx, o.WorkingDir); err != nil {
			return fmt.Errorf("error writing cache for %q: %w", jobRun.GetJobRunID(), err)
		}

		jobRun.ClearAllContent()
	}

	return nil
}
