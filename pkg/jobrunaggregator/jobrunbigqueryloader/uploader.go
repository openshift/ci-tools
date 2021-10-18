package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type getLastJobRunWithDataFunc func(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error)
type shouldCollectDataForJobFunc func(job jobrunaggregatorapi.JobRow) bool

func wantsTestRunData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectTestRuns
}
func wantsDisruptionData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectDisruption
}

type allJobsLoaderOptions struct {
	ciDataClient jobrunaggregatorlib.CIDataClient
	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	jobRunInserter BigQueryInserter

	shouldCollectedDataForJobFn shouldCollectDataForJobFunc
	getLastJobRunWithDataFn     getLastJobRunWithDataFunc
	jobRunUploader              uploader
}

func (o *allJobsLoaderOptions) Run(ctx context.Context) error {
	fmt.Printf("Locating jobs\n")

	jobs, err := o.ciDataClient.ListAllJobs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get jobs: %w", err)
	}

	fmt.Printf("Launching threads to upload test runs for %d jobs\n", len(jobs))

	waitGroup := sync.WaitGroup{}
	errCh := make(chan error, len(jobs))
	for i := range jobs {
		job := jobs[i]
		if !o.shouldCollectedDataForJobFn(job) {
			fmt.Printf("  skipping %q\n", job.JobName)
			continue
		}

		fmt.Printf("  launching %q\n", job.JobName)

		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()

			jobLoader := o.newJobBigQueryLoaderOptions(job)
			err := jobLoader.Run(ctx)
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

func (o *allJobsLoaderOptions) newJobBigQueryLoaderOptions(job jobrunaggregatorapi.JobRow) *jobLoaderOptions {

	return &jobLoaderOptions{
		jobName:                   job.JobName,
		ciDataClient:              o.ciDataClient,
		gcsClient:                 o.gcsClient,
		numberOfConcurrentReaders: 20,
		jobRunInserter:            o.jobRunInserter,
		getLastJobRunWithDataFn:   o.getLastJobRunWithDataFn,
		jobRunUploader:            o.jobRunUploader,
	}
}

// jobLoaderOptions
// 1. reads a local cache of prowjob.json and junit files for a particular job.
// 2. for every junit file
// 3. reads all junit for the each jobrun
// 4. constructs a synthentic junit that includes every test and assigns pass/fail to each test
type jobLoaderOptions struct {
	jobName string

	// CIDataClient is used to read the last inserted results for a job
	ciDataClient jobrunaggregatorlib.CIDataClient
	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	numberOfConcurrentReaders int64
	jobRunInserter            BigQueryInserter

	getLastJobRunWithDataFn getLastJobRunWithDataFunc
	jobRunUploader          uploader
}

func (o *jobLoaderOptions) Run(ctx context.Context) error {
	fmt.Printf("Analyzing job %q.\n", o.jobName)

	lastJobRun, err := o.getLastJobRunWithDataFn(ctx, o.jobName)
	if err != nil {
		return err
	}
	startingJobRunID := "0"
	if lastJobRun != nil {
		startingJobRunID = jobrunaggregatorlib.NextJobRunID(lastJobRun.Name)
	}

	jobRunProcessingCh, errorCh, err := o.gcsClient.ListJobRunNames(ctx, o.jobName, startingJobRunID)
	if err != nil {
		return err
	}

	insertionErrorLock := sync.Mutex{}
	insertionErrors := []error{}
	go func() {
		insertionErrorLock.Lock()
		defer insertionErrorLock.Unlock()

		// exits when the channel closes
		for err := range errorCh {
			insertionErrors = append(insertionErrors, err)
		}
	}()

	// we want to process the insertion in-order so we can choose to stop an upload on the first error
	lastDoneUploadingCh := make(chan struct{})
	concurrentWorkers := semaphore.NewWeighted(o.numberOfConcurrentReaders)
	currentUploaders := sync.WaitGroup{}
	close(lastDoneUploadingCh)
	for jobRunID := range jobRunProcessingCh {
		jobRunInserter := o.newJobRunBigQueryLoaderOptions(jobRunID, lastDoneUploadingCh)
		lastDoneUploadingCh = jobRunInserter.doneUploading

		if err := concurrentWorkers.Acquire(ctx, 1); err != nil {
			// this means the context is done
			return err
		}

		currentUploaders.Add(1)
		go func() {
			defer concurrentWorkers.Release(1)
			defer currentUploaders.Done()

			if err := jobRunInserter.Run(ctx); err != nil {
				errorCh <- err
			}
		}()
	}
	currentUploaders.Wait()

	// at this point we're done finding new jobs (jobRunProcessingCh is closed) and we've finished all jobRun insertions
	// (the waitGroup is done).  This means all error reporting is finished, so close the errorCh, then wait to complete
	// all the error gathering

	close(errorCh)
	insertionErrorLock.Lock()
	defer insertionErrorLock.Unlock()

	return utilerrors.NewAggregate(insertionErrors)
}

func (o *jobLoaderOptions) newJobRunBigQueryLoaderOptions(jobRunID string, readyToUpload chan struct{}) *jobRunLoaderOptions {
	return &jobRunLoaderOptions{
		jobName:        o.jobName,
		hobRunID:       jobRunID,
		gcsClient:      o.gcsClient,
		readyToUpload:  readyToUpload,
		jobRunInserter: o.jobRunInserter,
		doneUploading:  make(chan struct{}),
		jobRunUploader: o.jobRunUploader,
	}
}

type uploader interface {
	uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) error
}

// jobRunLoaderOptions
// 1. reads the GCS bucket for the job run
// 2. combines all junit for the job run
// 3. uploads all results to bigquery
type jobRunLoaderOptions struct {
	jobName  string
	hobRunID string

	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	readyToUpload  chan struct{}
	jobRunInserter BigQueryInserter
	doneUploading  chan struct{}

	jobRunUploader uploader
}

func (o *jobRunLoaderOptions) Run(ctx context.Context) error {
	defer close(o.doneUploading)

	fmt.Printf("Analyzing jobrun/%v/%v.\n", o.jobName, o.hobRunID)

	jobRun, err := o.readJobRunFromGCS(ctx)
	if err != nil {
		return err
	}

	// TODO we *could* read to see if we've already uploaded this.  That doesn't see necessary based on how
	//  we decide to pull the data to upload though.

	// wait until it is ready to upload before continuing
	select {
	case <-ctx.Done():
		return nil
	case <-o.readyToUpload:
	}

	if err := o.uploadJobRun(ctx, jobRun); err != nil {
		return fmt.Errorf("jobrun/%v/%v failed to upload to bigquery: %w", o.jobName, o.hobRunID, err)
	}

	return nil
}

func (o *jobRunLoaderOptions) uploadJobRun(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo) error {
	prowJob, err := jobRun.GetProwJob(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("uploading prowjob.yaml: jobrun/%v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	jobRunRow := newJobRunRow(jobRun, prowJob)
	if err := o.jobRunInserter.Put(ctx, jobRunRow); err != nil {
		return err
	}

	fmt.Printf("  uploading content: jobrun/%v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	if err := o.jobRunUploader.uploadContent(ctx, jobRun, prowJob); err != nil {
		return err
	}

	return nil
}

// associateJobRuns returns allJobRuns and currentAggregationTargetJobRuns
func (o *jobRunLoaderOptions) readJobRunFromGCS(ctx context.Context) (jobrunaggregatorapi.JobRunInfo, error) {
	jobRunInfo, err := o.gcsClient.ReadJobRunFromGCS(ctx, o.jobName, o.hobRunID)
	if err != nil {
		return nil, err
	}
	prowjob, err := jobRunInfo.GetProwJob(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get prowjob for jobrun/%v/%v: %w", o.jobName, o.hobRunID, err)
	}
	if prowjob.Status.CompletionTime == nil {
		fmt.Printf("Removing %q/%q because it isn't finished\n", o.jobName, o.hobRunID)
		return nil, nil
	}
	if _, err := jobRunInfo.GetAllContent(ctx); err != nil {
		return nil, fmt.Errorf("failed to get all content for jobrun/%v/%v: %w", o.jobName, o.hobRunID, err)
	}

	return jobRunInfo, nil
}
