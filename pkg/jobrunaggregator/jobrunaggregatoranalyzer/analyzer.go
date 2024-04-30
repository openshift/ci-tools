package jobrunaggregatoranalyzer

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"

	prowjobclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	"k8s.io/utils/clock"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

// JobRunAggregatorAnalyzerOptions
// 1. reads a local cache of prowjob.json and junit files for a particular job.
// 2. finds jobruns for the the specified payload tag
// 3. reads all junit for the each jobrun
// 4. constructs a synthentic junit that includes every test and assigns pass/fail to each test
type JobRunAggregatorAnalyzerOptions struct {
	jobRunLocator      jobrunaggregatorlib.JobRunLocator
	passFailCalculator baseline

	// explicitGCSPrefix is set to control the base path we search in GCSBuckets. If not set, the jobName will be used
	// to set a default value that usually works.
	explicitGCSPrefix string
	jobName           string
	payloadTag        string
	workingDir        string

	// jobRunStartEstimate is the time that we think the job runs we're aggregating started.
	// it should be within an hour, plus or minus.
	jobRunStartEstimate time.Time
	clock               clock.Clock
	timeout             time.Duration

	prowJobClient       *prowjobclientset.Clientset
	jobStateQuerySource string
	prowJobMatcherFunc  jobrunaggregatorlib.ProwJobMatcherFunc

	staticJobRunIdentifiers []jobrunaggregatorlib.JobRunIdentifier
	gcsBucket               string
}

func (o *JobRunAggregatorAnalyzerOptions) loadStaticJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
	var jobRuns []jobrunaggregatorapi.JobRunInfo
	for _, job := range o.staticJobRunIdentifiers {
		jobRun, err := o.jobRunLocator.FindJob(ctx, job.JobRunID)
		if err != nil {
			// Do not fail when one job fetch fails
			logrus.WithError(err).Errorf("error finding job %s", job.JobRunID)
			continue
		}
		if jobRun != nil {
			jobRuns = append(jobRuns, jobRun)
		}
	}
	return jobRuns, nil
}

func (o *JobRunAggregatorAnalyzerOptions) GetRelatedJobRunsFromIdentifiers(ctx context.Context, jobRunIdentifiers []jobrunaggregatorlib.JobRunIdentifier) ([]jobrunaggregatorapi.JobRunInfo, error) {
	o.staticJobRunIdentifiers = jobRunIdentifiers
	return o.GetRelatedJobRuns(ctx)
}

// GetRelatedJobRuns gets all related job runs for analysis
func (o *JobRunAggregatorAnalyzerOptions) GetRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
	// allow for the list of ids to be passed in
	if len(o.staticJobRunIdentifiers) > 0 {
		return o.loadStaticJobRuns(ctx)
	}

	errorsInARow := 0
	for {
		jobsToAggregate, err := o.jobRunLocator.FindRelatedJobs(ctx)
		if err == nil {
			return jobsToAggregate, nil
		}
		if err != nil {
			if errorsInARow > 20 {
				return nil, err
			}
			errorsInARow++
			logrus.WithError(err).Error("error finding jobs to aggregate")
		}

		logrus.Info("waiting and will attempt to find related jobs in one minute")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Minute):
			continue
		}
	}
}

func (o *JobRunAggregatorAnalyzerOptions) Run(ctx context.Context) error {
	// if it hasn't been more than two hours since the jobRuns started, the list isn't complete.
	readyAt := o.jobRunStartEstimate.Add(10 * time.Minute)

	// the aggregator has a long time.  The jobs it aggregates only have 4h (we think).
	durationToWait := o.timeout - 20*time.Minute
	if durationToWait > (5*time.Hour + 15*time.Minute) {
		durationToWait = 5*time.Hour + 15*time.Minute
	}
	timeToStopWaiting := o.jobRunStartEstimate.Add(durationToWait)
	alog := logrus.WithFields(logrus.Fields{
		"job":     o.jobName,
		"payload": o.payloadTag,
	})

	alog.WithFields(logrus.Fields{
		"now":       o.clock.Now().Format(time.RFC3339), // in tests, may not match the log timestamp
		"readyAt":   readyAt.Format(time.RFC3339),
		"timeoutAt": timeToStopWaiting.UTC().Format(time.RFC3339),
	}).Info("aggregating job runs")
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	currentAggregationDir := filepath.Join(o.workingDir, o.jobName, o.payloadTag)
	if err := os.MkdirAll(currentAggregationDir, 0755); err != nil {
		return fmt.Errorf("error creating destination directory %q: %w", currentAggregationDir, err)
	}

	err := jobrunaggregatorlib.WaitUntilTime(ctx, readyAt)
	if err != nil {
		return err
	}

	var jobRunWaiter jobrunaggregatorlib.JobRunWaiter
	if o.jobStateQuerySource == jobrunaggregatorlib.JobStateQuerySourceBigQuery || o.prowJobClient == nil {
		jobRunWaiter = &jobrunaggregatorlib.BigQueryJobRunWaiter{JobRunGetter: o, TimeToStopWaiting: timeToStopWaiting}
	} else {
		jobRunWaiter = &jobrunaggregatorlib.ClusterJobRunWaiter{
			ProwJobClient:      o.prowJobClient,
			TimeToStopWaiting:  timeToStopWaiting,
			ProwJobMatcherFunc: o.prowJobMatcherFunc,
		}
	}
	finishedJobsToAggregate, _, finishedJobRunNames, unfinishedJobNames, err := jobrunaggregatorlib.WaitAndGetAllFinishedJobRuns(ctx, o, jobRunWaiter, o.workingDir, "aggregated")
	if err != nil {
		return err
	}

	if len(unfinishedJobNames) > 0 {
		alog.Infof("found %d unfinished related jobRuns: %v", len(unfinishedJobNames), strings.Join(unfinishedJobNames, ", "))
	}
	// if more than three jobruns timed out, just fail the entire aggregation
	if len(unfinishedJobNames) > 3 {
		return fmt.Errorf("%s for %s: found %d unfinished related jobRuns: %v\n", o.jobName, o.payloadTag, len(unfinishedJobNames), strings.Join(unfinishedJobNames, ", "))
	}
	alog.Infof("aggregating %d related jobRuns: %v", len(finishedJobsToAggregate), strings.Join(finishedJobRunNames, ", "))

	aggregationConfiguration := &AggregationConfiguration{}
	for _, jobRunName := range unfinishedJobNames {
		jobRunGCSBucketRoot := filepath.Join("logs", o.jobName, jobRunName)
		if len(o.explicitGCSPrefix) > 0 {
			jobRunGCSBucketRoot = filepath.Join(o.explicitGCSPrefix, jobRunName)
		}
		aggregationConfiguration.FinishedJobs = append(
			aggregationConfiguration.FinishedJobs,
			JobRunInfo{
				JobName:      o.jobName,
				JobRunID:     jobRunName,
				HumanURL:     jobrunaggregatorapi.GetHumanURLForLocation(jobRunGCSBucketRoot, o.gcsBucket),
				GCSBucketURL: jobrunaggregatorapi.GetGCSArtifactURLForLocation(jobRunGCSBucketRoot, o.gcsBucket),
				Status:       "unknown",
			},
		)
	}

	currentAggregationJunit := &aggregatedJobRunJunit{
		jobGCSBucketRoot: filepath.Join("logs", o.jobName),
	}
	if len(o.explicitGCSPrefix) > 0 {
		currentAggregationJunit.jobGCSBucketRoot = o.explicitGCSPrefix
	}
	masterNodesUpdated := ""
	for i := range finishedJobsToAggregate {
		jobRun := finishedJobsToAggregate[i]

		// Initialize our junits and file names.
		// We aren't required to do this but if we
		// do we can catch any errors and bail.
		err := jobRun.GetJobRunFromGCS(ctx)
		if err != nil {
			return err
		}

		// We found a case where the first job failed to upgrade but the others didn't
		// original logic stopped on the first flag we found which indicated master nodes did not update
		// and led to lower disruption values being used, causing failures.
		// we now look at each job unless we have a 'Y' value already
		if strings.ToUpper(masterNodesUpdated) != "Y" {
			// get the flag to see if masternodes have been updated
			clusterData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "cluster-data")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Could not fetch cluster data for %s - %v\n", jobRun.GetJobRunID(), err)
			}
			updatedFlag := jobrunaggregatorlib.GetMasterNodesUpdatedStatusFromClusterData(clusterData)

			// if we have any value set it here
			// if we set a 'Y' here we won't come back in this loop based on the check above
			if len(updatedFlag) > 0 {
				masterNodesUpdated = updatedFlag
			}

		}
		currJunit, err := newJobRunJunit(ctx, jobRun)
		if err != nil {
			return err
		}
		prowJob, err := currJunit.jobRun.GetProwJob(ctx)
		if err != nil {
			return err
		}
		aggregationConfiguration.FinishedJobs = append(
			aggregationConfiguration.FinishedJobs,
			JobRunInfo{
				JobName:      jobRun.GetJobName(),
				JobRunID:     jobRun.GetJobRunID(),
				HumanURL:     jobRun.GetHumanURL(),
				GCSBucketURL: jobRun.GetGCSArtifactURL(),
				Status:       string(prowJob.Status.State),
			},
		)

		currentAggregationJunit.addJobRun(jobrunaggregatorlib.GetPayloadTagFromProwJob(prowJob), currJunit)
	}

	// write out the jobruns aggregated by this jobrun.
	aggregationConfigYAML, err := yaml.Marshal(aggregationConfiguration)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(currentAggregationDir, "aggregation-config.yaml"), aggregationConfigYAML, 0644); err != nil {
		return err
	}

	alog.Info("aggregating junit tests")
	currentAggregationJunitSuites, err := currentAggregationJunit.aggregateAllJobRuns()
	if err != nil {
		return err
	}
	if err := assignPassFail(ctx, o.jobName, currentAggregationJunitSuites, o.passFailCalculator); err != nil {
		return err
	}

	logrus.Infof("%q for %q:  aggregating disruption tests", o.jobName, o.payloadTag)

	disruptionSuite, err := o.CalculateDisruptionTestSuite(ctx, currentAggregationJunit.jobGCSBucketRoot, finishedJobsToAggregate, masterNodesUpdated)
	if err != nil {
		return err
	}
	currentAggregationJunitSuites.Suites = append(currentAggregationJunitSuites.Suites, disruptionSuite)

	// TODO this is the spot where we would add an alertSuite that aggregates the alerts firing in our clusters to prevent
	//  allowing more and more failing alerts through just because one fails.

	currentAggrationJunitXML, err := xml.Marshal(currentAggregationJunitSuites)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(currentAggregationDir, "junit-aggregated.xml"), currentAggrationJunitXML, 0644); err != nil {
		return err
	}

	logrus.Infof("%q for %q:  Done aggregating", o.jobName, o.payloadTag)

	// now scan for a failure
	fakeSuite := &junit.TestSuite{Children: currentAggregationJunitSuites.Suites}
	jobrunaggregatorlib.OutputTestCaseFailures([]string{"root"}, fakeSuite)

	summaryHTML := htmlForTestRuns(o.jobName, fakeSuite)
	if err := os.WriteFile(filepath.Join(o.workingDir, "aggregation-testrun-summary.html"), []byte(summaryHTML), 0644); err != nil {
		return err
	}

	if hasFailedTestCase(fakeSuite) {
		// we already indicated failure messages above
		return fmt.Errorf("Some tests failed aggregation.  See above for details.")
	}

	return nil
}

func hasFailedTestCase(suite *junit.TestSuite) bool {
	for _, testCase := range suite.TestCases {
		if testCase.FailureOutput != nil {
			return true
		}
	}

	for _, child := range suite.Children {
		if hasFailedTestCase(child) {
			return true
		}
	}

	return false
}
