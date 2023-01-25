package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type shouldCollectDataForJobFunc func(job jobrunaggregatorapi.JobRow) bool

func wantsTestRunData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectTestRuns
}
func wantsDisruptionData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectDisruption
}

type allJobsLoaderOptions struct {
	ciDataClient jobrunaggregatorlib.JobLister
	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	jobRunInserter jobrunaggregatorlib.BigQueryInserter

	shouldCollectedDataForJobFn shouldCollectDataForJobFunc
	jobRunUploader              uploader
	logLevel                    string
}

func (o *allJobsLoaderOptions) Run(ctx context.Context) error {
	start := time.Now()

	// Set log level
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		logrus.WithError(err).Fatal("Cannot parse log-level")
	}
	logrus.SetLevel(level)

	logrus.Infof("Locating jobs")

	jobs, err := o.ciDataClient.ListAllJobs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get jobs: %w", err)
	}

	// Convert list of JobRows to a map by job name, we're going to want quick lookups
	jobRowsMap := map[string]jobrunaggregatorapi.JobRow{}
	for _, job := range jobs {
		jobRowsMap[job.JobName] = job
	}

	jobCount := len(jobs)

	lastUploadedJobEndTime, err := o.jobRunUploader.getLastUploadedJobRunEndTime(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last job run end time: %w", err)
	}
	logrus.WithField("lastUploadedJobRun", lastUploadedJobEndTime).Info("got last uploaded job run end time")

	// Handle the very unlikely case where it's a fresh db and we got no last uploaded job run end time:
	if lastUploadedJobEndTime.IsZero() {
		logrus.Warn("got an empty lastUploadedJobRun time, importing past two weeks of job runs")
		t := time.Now().Add(-14 * 24 * time.Hour)
		lastUploadedJobEndTime = &t
	}

	// Subtract 30 min from our last upload, we're going to list all prow jobs ending this amount prior
	// to our last import just incase jobs get inserted slightly out of order from their actual recorded end time.
	listProwJobsSince := lastUploadedJobEndTime.Add(-30 * time.Minute)
	logrus.WithField("since", listProwJobsSince).Info("listing prow jobs since")

	// Lookup the known prow job IDs (already uploaded) that ended within this window. BigQuery does not
	// prevent us from inserting duplicate rows, we have to do it ourselves. We'll compare
	// each incoming prow job to make sure it's not in the list we've already inserted.
	existingJobRunIDs, err := o.jobRunUploader.listUploadedJobRunIDsSince(ctx, &listProwJobsSince)
	if err != nil {
		return fmt.Errorf("error listing uploaded job run IDs: %w", err)
	}
	logrus.WithField("idCount", len(existingJobRunIDs)).Info("found existing job run IDs")

	// Lookup the jobs that have run and we may need to import. There will be some overlap with what we already have.
	jobRunsToImport, err := o.ciDataClient.ListProwJobRunsSince(ctx, &listProwJobsSince)
	if err != nil {
		return fmt.Errorf("error listing job runs to import: %w", err)
	}
	logrus.WithField("importCount", len(jobRunsToImport)).Info("found job runs to import")

	errs := []error{}
	/*
		wg := sync.WaitGroup{}
		logrus.Infof("Launching threads to upload test runs for %d jobs", jobCount)
		errChan := make(chan error, jobCount)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go o.processJobs(ctx, &wg, i, jobCount, jobChan, errChan)
		}

		wg.Wait()
		logrus.Infof("WaitGroup completed")
		close(errChan)


		for e := range errChan {
			logrus.WithError(e).Error("error encountered during upload")
			errs = append(errs, e)
		}
	*/

	duration := time.Now().Sub(start)
	logrus.WithFields(logrus.Fields{
		"jobs":     jobCount,
		"duration": duration,
		"errors":   len(errs),
	}).Info("completed upload")

	return utilerrors.NewAggregate(errs)
}

// processJobs is started in several concurrent goroutines to pull jobs to process from the jobChan. Errors are sent
// to the errChan for aggregation in the main thread.
func (o *allJobsLoaderOptions) processJobs(ctx context.Context, wg *sync.WaitGroup, workerThread, jobCount int, jobChan <-chan jobrunaggregatorapi.JobRow, errChan chan<- error) {
	defer wg.Done()
	for job := range jobChan {
		jobLogger := logrus.WithFields(logrus.Fields{
			"job":    job.JobName,
			"worker": workerThread,
		})
		// log how many job remain to be processed
		jobLogger.WithField("remaining", fmt.Sprintf("%d/%d", len(jobChan), jobCount)).Info("pulled job from queue")

		if !o.shouldCollectedDataForJobFn(job) {
			jobLogger.Info("skipping job")
			continue
		}

		jobLoader := o.newJobBigQueryLoaderOptions(job, jobLogger)
		err := jobLoader.Run(ctx)
		if err != nil {
			jobLogger.WithError(err).Error("error uploading job runs for job")
			errChan <- err
		}
	}
	logrus.WithField("worker", workerThread).Info("worker thread complete")
}
func (o *allJobsLoaderOptions) newJobBigQueryLoaderOptions(job jobrunaggregatorapi.JobRow, logger logrus.FieldLogger) *jobLoaderOptions {

	return &jobLoaderOptions{
		jobName:                   job.JobName,
		gcsClient:                 o.gcsClient,
		numberOfConcurrentReaders: 20,
		jobRunInserter:            o.jobRunInserter,
		jobRunUploader:            o.jobRunUploader,
		logger:                    logger,
	}
}

// jobLoaderOptions
// 1. reads a local cache of prowjob.json and junit files for a particular job.
// 2. for every junit file
// 3. reads all junit for the each jobrun
// 4. constructs a synthentic junit that includes every test and assigns pass/fail to each test
type jobLoaderOptions struct {
	jobName string

	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	numberOfConcurrentReaders int64
	jobRunInserter            jobrunaggregatorlib.BigQueryInserter

	jobRunUploader uploader

	logger logrus.FieldLogger
}

func (o *jobLoaderOptions) Run(ctx context.Context) error {

	o.logger.Info("processing job")
	lastJobRun, err := o.jobRunUploader.getLastUploadedJobRunForJob(ctx, o.jobName)
	if err != nil {
		return err
	}
	o.logger.WithField("lastJobRun", lastJobRun).Info("found last job run")
	startingJobRunID := "0"
	if lastJobRun != nil {
		startingJobRunID = jobrunaggregatorlib.NextJobRunID(lastJobRun.Name)
	}
	o.logger.WithField("startingJobRunID", startingJobRunID).Info("startingJobRunID")

	jobRunProcessingCh, errorCh, err := o.gcsClient.ListJobRunNamesOlderThanFourHours(ctx, o.jobName, startingJobRunID, o.logger)
	if err != nil {
		return err
	}
	o.logger.Info("job run processing channels established")

	insertionErrorLock := sync.Mutex{}
	insertionErrors := []error{}
	go func() {
		insertionErrorLock.Lock()
		defer insertionErrorLock.Unlock()

		// exits when the channel closes
		for err := range errorCh {
			o.logger.WithError(err).Error("insertion error")
			insertionErrors = append(insertionErrors, err)
		}
	}()

	// we want to process the insertion in-order so we can choose to stop an upload on the first error
	lastDoneUploadingCh := make(chan struct{})
	concurrentWorkers := semaphore.NewWeighted(o.numberOfConcurrentReaders)
	currentUploaders := sync.WaitGroup{}
	close(lastDoneUploadingCh)
	for jobRunID := range jobRunProcessingCh {
		jobRunInserter := o.newJobRunBigQueryLoaderOptions(jobRunID, lastDoneUploadingCh, o.logger)
		lastDoneUploadingCh = jobRunInserter.doneUploading
		jrLogger := o.logger.WithField("jobRun", jobRunID)

		jrLogger.Info("acquiring concurrentWorker")
		if err := concurrentWorkers.Acquire(ctx, 1); err != nil {
			jrLogger.WithError(err).Info("context is done")
			// this means the context is done
			return err
		}
		jrLogger.Info("concurrentWorker acquired")

		currentUploaders.Add(1)
		go func(jrID string) {
			defer concurrentWorkers.Release(1)
			defer jrLogger.Info("concurrentWorker released")
			defer currentUploaders.Done()
			defer jrLogger.Info("concurrentUploader Done")

			jrLogger.WithField("jobRunID", jrID).Info("inserting job run")
			if err := jobRunInserter.Run(ctx); err != nil {
				jrLogger.WithField("jobRun", jrID).WithError(err).Error("error inserting job run")
				errorCh <- err
			}
		}(jobRunID)
		jrLogger.Info("finished processing job run")
	}
	currentUploaders.Wait()

	// at this point we're done finding new jobs (jobRunProcessingCh is closed) and we've finished all jobRun insertions
	// (the waitGroup is done).  This means all error reporting is finished, so close the errorCh, then wait to complete
	// all the error gathering
	o.logger.Info("completed processing job runs")

	close(errorCh)
	insertionErrorLock.Lock()
	defer insertionErrorLock.Unlock()

	return utilerrors.NewAggregate(insertionErrors)
}

func (o *jobLoaderOptions) newJobRunBigQueryLoaderOptions(jobRunID string, readyToUpload chan struct{}, logger logrus.FieldLogger) *jobRunLoaderOptions {
	return &jobRunLoaderOptions{
		jobName:        o.jobName,
		jobRunID:       jobRunID,
		gcsClient:      o.gcsClient,
		readyToUpload:  readyToUpload,
		jobRunInserter: o.jobRunInserter,
		doneUploading:  make(chan struct{}),
		jobRunUploader: o.jobRunUploader,
		logger:         logger.WithField("jobRun", jobRunID),
	}
}

type uploader interface {
	uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob, logger logrus.FieldLogger) error
	getLastUploadedJobRunForJob(ctx context.Context, jobName string) (*jobrunaggregatorapi.JobRunRow, error)
	getLastUploadedJobRunEndTime(ctx context.Context) (*time.Time, error)
	listUploadedJobRunIDsSince(ctx context.Context, since *time.Time) ([]string, error)
}

// jobRunLoaderOptions
// 1. reads the GCS bucket for the job run
// 2. combines all junit for the job run
// 3. uploads all results to bigquery
type jobRunLoaderOptions struct {
	jobName  string
	jobRunID string

	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	readyToUpload  chan struct{}
	jobRunInserter jobrunaggregatorlib.BigQueryInserter
	doneUploading  chan struct{}

	jobRunUploader uploader
	logger         logrus.FieldLogger
}

func (o *jobRunLoaderOptions) Run(ctx context.Context) error {
	defer close(o.doneUploading)

	o.logger.Debug("Analyzing jobrun")

	jobRun, err := o.readJobRunFromGCS(ctx)
	if err != nil {
		o.logger.WithError(err).Error("error reading job run from GCS")
		return err
	}
	// this can happen if there is no prowjob.json, so no work to do.
	if jobRun == nil {
		return nil
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
		return fmt.Errorf("jobrun/%v/%v failed to upload to bigquery: %w", o.jobName, o.jobRunID, err)
	}

	return nil
}

func (o *jobRunLoaderOptions) uploadJobRun(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo) error {
	prowJob, err := jobRun.GetProwJob(ctx)
	if err != nil {
		return err
	}
	o.logger.Info("uploading prowjob.yaml")
	jobRunRow := newJobRunRow(jobRun, prowJob)
	if err := o.jobRunInserter.Put(ctx, jobRunRow); err != nil {
		o.logger.WithError(err).Error("error inserting job run row")
		return err
	}

	o.logger.Infof("uploading content for jobrun")
	if err := o.jobRunUploader.uploadContent(ctx, jobRun, prowJob, o.logger); err != nil {
		o.logger.WithError(err).Error("error uploading content")
		return err
	}

	return nil
}

// associateJobRuns returns allJobRuns and currentAggregationTargetJobRuns
func (o *jobRunLoaderOptions) readJobRunFromGCS(ctx context.Context) (jobrunaggregatorapi.JobRunInfo, error) {
	jobRunInfo, err := o.gcsClient.ReadJobRunFromGCS(ctx, "logs/"+o.jobName, o.jobName, o.jobRunID, o.logger)
	if err != nil {
		o.logger.WithError(err).Error("error in ReadJobRunFromGCS")
		return nil, err
	}
	// this can happen if there is no prowjob.json
	if jobRunInfo == nil {
		o.logger.Debug("no prowjob.json found")
		return nil, nil
	}
	prowjob, err := jobRunInfo.GetProwJob(ctx)
	if err != nil {
		o.logger.WithError(err).Error("error in GetProwJob")
		return nil, fmt.Errorf("failed to get prowjob for jobrun/%v/%v: %w", o.jobName, o.jobRunID, err)
	}
	if prowjob.Status.CompletionTime == nil {
		o.logger.Info("Removing job run because it isn't finished")
		return nil, nil
	}
	if _, err := jobRunInfo.GetAllContent(ctx); err != nil {
		o.logger.WithError(err).Error("error getting all content for jobrun")
		return nil, fmt.Errorf("failed to get all content for jobrun/%v/%v: %w", o.jobName, o.jobRunID, err)
	}

	return jobRunInfo, nil
}
