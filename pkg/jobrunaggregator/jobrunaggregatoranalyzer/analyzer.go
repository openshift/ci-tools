package jobrunaggregatoranalyzer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/junit"

	"sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

// JobRunAggregatorAnalyzerOptions
// 1. reads a local cache of prowjob.json and junit files for a particular job.
// 2. finds jobruns for the the specified payload tag
// 3. reads all junit for the each jobrun
// 4. constructs a synthentic junit that includes every test and assigns pass/fail to each test
type JobRunAggregatorAnalyzerOptions struct {
	jobRunLocator      jobrunaggregatorlib.JobRunLocator
	passFailCalculator baseline

	jobName    string
	payloadTag string
	workingDir string

	// jobRunStartEstimate is the time that we think the job runs we're aggregating started.
	// it should be within an hour, plus or minus.
	jobRunStartEstimate time.Time
	clock               clock.Clock
	timeout             time.Duration
}

func (o *JobRunAggregatorAnalyzerOptions) getRelatedJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
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
			fmt.Printf("error finding jobs to aggregate: %v", err)
		}

		fmt.Printf("   waiting and will attempt to find related jobs in a minute\n")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Minute):
			continue
		}
	}

	return nil, fmt.Errorf("how on earth did we get here??")
}

func (o *JobRunAggregatorAnalyzerOptions) Run(ctx context.Context) error {
	// if it hasn't been more than hour since the jobRuns started, the list isn't complete.
	readyAt := o.jobRunStartEstimate.Add(1 * time.Hour)

	fmt.Printf("Aggregating job runs of type %q for %q.  ReadyAt=%v, now=%v.\n", o.jobName, o.payloadTag, readyAt, o.clock.Now())
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	currentAggregationDir := filepath.Join(o.workingDir, o.jobName, o.payloadTag)
	if err := os.MkdirAll(currentAggregationDir, 0755); err != nil {
		return fmt.Errorf("error creating destination directory %q: %w", currentAggregationDir, err)
	}

	finishedJobsToAggregate := []jobrunaggregatorapi.JobRunInfo{}
	finishedJobRunNames := []string{}
	for { // TODO extract to a method.
		fmt.Println() // for prettier logs
		// reset vars
		finishedJobsToAggregate = []jobrunaggregatorapi.JobRunInfo{}
		finishedJobRunNames = []string{}

		relatedJobs, err := o.getRelatedJobs(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%q for %q: found %d related jobRuns.\n", o.jobName, o.payloadTag, len(relatedJobs))

		if o.clock.Now().Before(readyAt) {
			fmt.Printf("%q for %q: waiting until %v to collect more jobRuns before assessing finished or not. (now=%v)\n", o.jobName, o.payloadTag, readyAt, o.clock.Now())
			time.Sleep(2 * time.Minute)
			continue
		}
		fmt.Printf("%q for %q: it is %v, finished waiting until %v.\n", o.jobName, o.payloadTag, o.clock.Now(), readyAt)
		if len(relatedJobs) == 0 {
			return fmt.Errorf("%q for %q: found no related jobRuns", o.jobName, o.payloadTag)
		}

		unfinishedJobNames := []string{}
		for i := range relatedJobs {
			relatedJob := relatedJobs[i]
			if !relatedJob.IsFinished(ctx) {
				fmt.Printf("%v/%v is not finished\n", relatedJob.GetJobName(), relatedJob.GetJobRunID())
				unfinishedJobNames = append(unfinishedJobNames, relatedJob.GetJobRunID())
				continue
			}

			prowJob, err := relatedJob.GetProwJob(ctx)
			if err != nil {
				fmt.Printf("  error reading prowjob %v: %v\n", relatedJob.GetJobRunID(), err)
				unfinishedJobNames = append(unfinishedJobNames, relatedJob.GetJobRunID())
			}

			if prowJob.Status.CompletionTime == nil {
				fmt.Printf("%v/%v has no completion time for resourceVersion=%v\n", relatedJob.GetJobName(), relatedJob.GetJobRunID(), prowJob.ResourceVersion)
				unfinishedJobNames = append(unfinishedJobNames, relatedJob.GetJobRunID())
				continue
			}
			finishedJobsToAggregate = append(finishedJobsToAggregate, relatedJob)
			finishedJobRunNames = append(finishedJobRunNames, relatedJob.GetJobRunID())
		}

		if len(unfinishedJobNames) > 0 {
			fmt.Printf("%q for %q: found %d unfinished related jobRuns: %v\n", o.jobName, o.payloadTag, len(unfinishedJobNames), strings.Join(unfinishedJobNames, ", "))
			time.Sleep(2 * time.Minute)
			continue
		}

		break
	}

	fmt.Printf("%q for %q: aggregating %d related jobRuns: %v\n", o.jobName, o.payloadTag, len(finishedJobsToAggregate), strings.Join(finishedJobRunNames, ", "))

	aggregationConfiguration := &AggregationConfiguration{}
	currentAggregationJunit := &aggregatedJobRunJunit{}
	for i := range finishedJobsToAggregate {
		jobRun := finishedJobsToAggregate[i]
		currJunit, err := newJobRunJunit(ctx, jobRun)
		if err != nil {
			return err
		}
		prowJob, err := currJunit.jobRun.GetProwJob(ctx)
		if err != nil {
			return err
		}
		aggregationConfiguration.IndividualJobs = append(
			aggregationConfiguration.IndividualJobs,
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

	fmt.Printf("%q for %q:  aggregating junit tests.\n", o.jobName, o.payloadTag)
	currentAggregationJunitSuites, err := currentAggregationJunit.aggregateAllJobRuns()
	if err != nil {
		return err
	}
	if err := assignPassFail(ctx, currentAggregationJunitSuites, o.passFailCalculator); err != nil {
		return err
	}
	currentAggrationJunitXML, err := xml.Marshal(currentAggregationJunitSuites)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(currentAggregationDir, "junit-aggregated.xml"), currentAggrationJunitXML, 0644); err != nil {
		return err
	}

	aggregationConfigYAML, err := yaml.Marshal(aggregationConfiguration)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(currentAggregationDir, "aggregation-config.yaml"), aggregationConfigYAML, 0644); err != nil {
		return err
	}

	fmt.Printf("%q for %q:  Done aggregating.\n", o.jobName, o.payloadTag)

	// now scan for a failure
	fakeSuite := &junit.TestSuite{Children: currentAggregationJunitSuites.Suites}
	outputTestCaseFailures([]string{"root"}, fakeSuite)

	if hasFailedTestCase(fakeSuite) {
		// we already indicated failure messages above
		return fmt.Errorf("Some tests failed aggregation.  See above for details.")
	}

	return nil
}

func outputTestCaseFailures(parents []string, suite *junit.TestSuite) {
	currSuite := append(parents, suite.Name)
	for _, testCase := range suite.TestCases {
		if testCase.FailureOutput == nil {
			continue
		}
		if len(testCase.FailureOutput.Output) == 0 && len(testCase.FailureOutput.Message) == 0 {
			continue
		}
		fmt.Printf("Test Failed! suite=[%s], testCase=%v\nMessage: %v\n%v\n\n",
			strings.Join(currSuite, "  "),
			testCase.Name,
			testCase.FailureOutput.Message,
			testCase.SystemOut)
	}

	for _, child := range suite.Children {
		outputTestCaseFailures(currSuite, child)
	}
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
