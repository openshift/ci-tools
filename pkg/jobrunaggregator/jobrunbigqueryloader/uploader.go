package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

const (
	// workerCount is the number of goroutines we run for concurrently importing job runs.
	// This bounds both our access to reading artifacts from GCS, as well as our writes
	// to BigQuery.
	workerCount = 10
)

type shouldCollectDataForJobFunc func(job jobrunaggregatorapi.JobRow) bool

func wantsTestRunData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectTestRuns
}
func wantsDisruptionData(job jobrunaggregatorapi.JobRow) bool {
	return job.CollectDisruption
}

type JobRunUploaderRegistry struct {
	JobRunUploaders map[string]uploader
}

func (j *JobRunUploaderRegistry) Register(name string, jobRunUploader uploader) {
	if j.JobRunUploaders == nil {
		j.JobRunUploaders = make(map[string]uploader)
	}

	j.JobRunUploaders[name] = jobRunUploader
}

func (j *JobRunUploaderRegistry) Deregister(name string) {
	delete(j.JobRunUploaders, name)
}

type allJobsLoaderOptions struct {
	ciDataClient jobrunaggregatorlib.JobLister
	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	jobRunInserter jobrunaggregatorlib.BigQueryInserter

	shouldCollectedDataForJobFn shouldCollectDataForJobFunc
	jobRunUploaderRegistry      JobRunUploaderRegistry
	pendingUploadJobsLister     pendingUploadLister
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

	lastUploadedJobEndTime, err := o.pendingUploadJobsLister.getLastUploadedJobRunEndTime(ctx)
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
	// to our last import just in case jobs get inserted slightly out of order from their actual recorded end time.
	listProwJobsSince := lastUploadedJobEndTime.Add(-30 * time.Minute)
	logrus.WithField("since", listProwJobsSince).Info("listing prow jobs since")

	// Lookup the known prow job IDs (already uploaded) that ended within this window. BigQuery does not
	// prevent us from inserting duplicate rows, we have to do it ourselves. We'll compare
	// each incoming prow job to make sure it's not in the list we've already inserted.
	existingJobRunIDs, err := o.pendingUploadJobsLister.listUploadedJobRunIDsSince(ctx, &listProwJobsSince)
	if err != nil {
		return fmt.Errorf("error listing uploaded job run IDs: %w", err)
	}
	logrus.WithField("idCount", len(existingJobRunIDs)).Info("found existing job run IDs")

	// Lookup the jobs that have run and we may need to import. There will be some overlap with what we already have.
	jobRunsToImport, err := o.ciDataClient.ListProwJobRunsSince(ctx, &listProwJobsSince)
	if err != nil {
		return fmt.Errorf("error listing job runs to import: %w", err)
	}
	logrus.WithField("recentJobRuns", len(jobRunsToImport)).Info("found job runs to potentially import")

	// Populate a channel with all the job runs we want to import, worker threads will pull
	// from here until there's nothing left.
	jobRunsToImportCh := make(chan *jobrunaggregatorapi.TestPlatformProwJobRow, jobCount)
	for i := range jobRunsToImport {
		jr := jobRunsToImport[i]

		// skip if the run is not from a job we care about:
		jobRow, ok := jobRowsMap[jr.JobName]
		if !ok {
			logrus.WithFields(logrus.Fields{"job": jr.JobName, "run": jr.BuildID}).Debug("skipping job run for job not in our table")
			continue
		}

		if !o.shouldCollectedDataForJobFn(jobRow) {
			logrus.WithFields(logrus.Fields{"job": jr.JobName, "run": jr.BuildID}).Debug("skipping job run for job we do not import this type of data for")
			continue
		}

		// skip if we already have it:
		if _, ok := existingJobRunIDs[jr.BuildID]; ok {
			logrus.WithFields(logrus.Fields{"job": jr.JobName, "run": jr.BuildID}).Debug("skipping job run we already have imported")
			continue
		}
		jobRunsToImportCh <- jr
	}
	close(jobRunsToImportCh)
	runsToImportCount := len(jobRunsToImportCh)
	logrus.WithField("runsToImport", runsToImportCount).Info("job runs to import after filtering")

	errs := []error{}

	logrus.WithField("workers", workerCount).Info("Launching goroutines for concurrent uploads")
	wg := sync.WaitGroup{}
	errChan := make(chan error, jobCount)
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go o.processJobRuns(ctx, jobRowsMap, &wg, i, runsToImportCount, jobRunsToImportCh, errChan)
	}

	wg.Wait()
	logrus.Infof("WaitGroup completed")
	close(errChan)

	for e := range errChan {
		logrus.WithError(e).Error("error encountered during upload")
		errs = append(errs, e)
	}

	duration := time.Since(start)
	logrus.WithFields(logrus.Fields{
		"duration": duration,
		"errors":   len(errs),
	}).Info("completed upload")

	return utilerrors.NewAggregate(errs)
}

// processJobRuns is started in several concurrent goroutines to pull job runs to process from the channel. Errors are sent
// to the errChan for aggregation in the main thread.
func (o *allJobsLoaderOptions) processJobRuns(ctx context.Context, jobsMap map[string]jobrunaggregatorapi.JobRow, wg *sync.WaitGroup, workerThread, origRunsToImportCount int, jobRunsToImportCh <-chan *jobrunaggregatorapi.TestPlatformProwJobRow, errChan chan<- error) {
	defer wg.Done()
	for job := range jobRunsToImportCh {
		jrLogger := logrus.WithFields(logrus.Fields{
			"worker":   workerThread,
			"job":      job.JobName,
			"run":      job.BuildID,
			"progress": fmt.Sprintf("%d/%d", origRunsToImportCount-len(jobRunsToImportCh), origRunsToImportCount),
		})
		// log how many job runs remain to be processed
		jrLogger.Info("pulled job run from queue")

		jobRunInserter := o.newJobRunBigQueryLoaderOptions(job.JobName, job.BuildID,
			jobsMap[job.JobName].Release, jrLogger)
		if err := jobRunInserter.Run(ctx); err != nil {
			jrLogger.WithError(err).Error("error inserting job run")
			errChan <- err
		}
		jrLogger.Info("finished processing job run")
	}
	logrus.WithField("worker", workerThread).Info("worker thread complete")
}

func (o *allJobsLoaderOptions) newJobRunBigQueryLoaderOptions(jobName, jobRunID, jobRelease string, logger logrus.FieldLogger) *jobRunLoaderOptions {
	return &jobRunLoaderOptions{
		jobName:                jobName,
		jobRunID:               jobRunID,
		jobRelease:             jobRelease,
		gcsClient:              o.gcsClient,
		jobRunInserter:         o.jobRunInserter,
		jobRunUploaderRegistry: o.jobRunUploaderRegistry,
		logger:                 logger.WithField("jobRun", jobRunID),
	}
}

// uploader encapsulates the logic for lookups and uploads specific to each type of content we ingest. (disruption, alerting, test runs, etc)
type uploader interface {
	uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, release string, jobRunRow *jobrunaggregatorapi.JobRunRow, logger logrus.FieldLogger) error
}

// pendingUploadLister provides methods used to determine
// when the beginning of the newly available jobs should start
// and what jobs since the time specified are available.
// the time from getLastUploadedJobRunEndTime may not be the exact
// time provided to listUploadedJobRunIDsSince as overlap may be added
// to ensure records are not missed
type pendingUploadLister interface {
	// getLastUploadedJobRunEndTime gets the last known job that was uploaded from the implementations back end
	getLastUploadedJobRunEndTime(ctx context.Context) (*time.Time, error)

	// listUploadedJobRunIDsSince lists jobs available since the time specified
	listUploadedJobRunIDsSince(ctx context.Context, since *time.Time) (map[string]bool, error)
}

// jobRunLoaderOptions
// 1. reads the GCS bucket for the job run
// 2. combines all junit for the job run
// 3. uploads all results to bigquery
type jobRunLoaderOptions struct {
	jobName    string
	jobRunID   string
	jobRelease string

	// GCSClient is used to read the prowjob data
	gcsClient jobrunaggregatorlib.CIGCSClient

	jobRunInserter jobrunaggregatorlib.BigQueryInserter

	jobRunUploaderRegistry JobRunUploaderRegistry
	logger                 logrus.FieldLogger
}

func (o *jobRunLoaderOptions) Run(ctx context.Context) error {

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

	// Initialize our junits and file names.
	// We aren't required to do this but if we
	// do we can catch any errors and bail.
	err = jobRun.GetJobRunFromGCS(ctx)
	if err != nil {
		o.logger.WithError(err).Error("error getting job run from GCS")
		return err
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
	o.logger.Info("inserting job run row")

	clusterData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "cluster-data")
	if err != nil {
		// log but continue on
		o.logger.WithError(err).Error("error getting cluster-data in GetOpenShiftTestsFilesWithPrefix")
	}
	masterNodesUpdated := jobrunaggregatorlib.GetMasterNodesUpdatedStatusFromClusterData(clusterData)

	jobRunRow := newJobRunRow(jobRun, prowJob, masterNodesUpdated)
	if err := o.jobRunInserter.Put(ctx, jobRunRow); err != nil {
		o.logger.WithError(err).Error("error inserting job run row")
		return err
	}

	o.logger.Infof("uploading content for jobrun")
	for name, jobRunUploader := range o.jobRunUploaderRegistry.JobRunUploaders {
		if err := jobRunUploader.uploadContent(ctx, jobRun, o.jobRelease, jobRunRow, o.logger); err != nil {
			o.logger.WithError(err).Errorf("error uploading content for: %s", name)
		}
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

	return jobRunInfo, nil
}
