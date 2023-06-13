package jobrunaggregatorlib

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/utils/clock"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

type JobRunGetter interface {
	// GetRelatedJobRuns gets all related job runs for analysis
	GetRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error)
}

// WaitUntilTime waits until readAt time has passed
func WaitUntilTime(ctx context.Context, readyAt time.Time) error {
	fmt.Printf("Waiting now=%v, ReadyAt=%v.\n", time.Now(), readyAt)

	if time.Now().After(readyAt) {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(readyAt)):
		break
	}
	fmt.Printf("it is %v, finished waiting until %v.\n", time.Now(), readyAt)
	return nil
}

// WaitAndGetAllFinishedJobRuns waits for all job runs to finish until timeToStopWaiting. It returns all finished and unfinished job runs
func WaitAndGetAllFinishedJobRuns(ctx context.Context,
	timeToStopWaiting time.Time,
	jobRunGetter JobRunGetter,
	outputDir string,
	variantInfo string) ([]jobrunaggregatorapi.JobRunInfo, []jobrunaggregatorapi.JobRunInfo, []string, []string, error) {
	clock := clock.RealClock{}

	var finishedJobRuns []jobrunaggregatorapi.JobRunInfo
	var finishedJobRunNames []string
	var unfinishedJobRuns []jobrunaggregatorapi.JobRunInfo
	var unfinishedJobRunNames []string
	for {
		fmt.Println() // for prettier logs
		// reset vars
		finishedJobRuns = []jobrunaggregatorapi.JobRunInfo{}
		unfinishedJobRuns = []jobrunaggregatorapi.JobRunInfo{}
		finishedJobRunNames = []string{}
		unfinishedJobRunNames = []string{}

		relatedJobRuns, err := jobRunGetter.GetRelatedJobRuns(ctx)

		if err != nil {
			return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, err
		}

		if len(relatedJobRuns) == 0 {
			return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, fmt.Errorf("found no related jobRuns")
		}

		for i := range relatedJobRuns {
			jobRun := relatedJobRuns[i]
			if !jobRun.IsFinished(ctx) {
				fmt.Printf("%v/%v is not finished\n", jobRun.GetJobName(), jobRun.GetJobRunID())
				unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
				unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
				continue
			}

			prowJob, err := jobRun.GetProwJob(ctx)
			if err != nil {
				fmt.Printf("  error reading prowjob %v: %v\n", jobRun.GetJobRunID(), err)
				unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
				unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
				continue
			}

			if prowJob.Status.CompletionTime == nil {
				fmt.Printf("%v/%v has no completion time for resourceVersion=%v\n", jobRun.GetJobName(), jobRun.GetJobRunID(), prowJob.ResourceVersion)
				unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
				unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
				continue
			}
			finishedJobRuns = append(finishedJobRuns, jobRun)
			finishedJobRunNames = append(finishedJobRunNames, jobRun.GetJobName()+jobRun.GetJobRunID())
		}

		summaryHTML := htmlForJobRuns(ctx, finishedJobRuns, unfinishedJobRuns, variantInfo)
		if err := ioutil.WriteFile(filepath.Join(outputDir, "job-run-summary.html"), []byte(summaryHTML), 0644); err != nil {
			return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, err
		}

		// ready or not, it's time to check
		if clock.Now().After(timeToStopWaiting) {
			fmt.Printf("waited long enough. Ready or not, here I come. (readyOrNot=%v now=%v)\n", timeToStopWaiting, clock.Now())
			break
		}

		if len(unfinishedJobRunNames) > 0 {
			fmt.Printf("found %d unfinished related jobRuns: %v\n", len(unfinishedJobRunNames), strings.Join(unfinishedJobRunNames, ", "))
			select {
			case <-time.After(10 * time.Minute):
				continue
			case <-ctx.Done():
				return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, ctx.Err()
			}
		}

		break
	}

	fmt.Printf("found %d finished jobRuns: %v and %d unfinished jobRuns: %v\n",
		len(finishedJobRunNames), strings.Join(finishedJobRunNames, ", "), len(unfinishedJobRunNames), strings.Join(unfinishedJobRunNames, ", "))
	return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, nil
}

// OutputTestCaseFailures prints detailed test failures
func OutputTestCaseFailures(parents []string, suite *junit.TestSuite) {
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
		OutputTestCaseFailures(currSuite, child)
	}
}
