package jobrunaggregatorlib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/clock"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobclientset "sigs.k8s.io/prow/pkg/client/clientset/versioned"
	prowjobinformers "sigs.k8s.io/prow/pkg/client/informers/externalversions"
	v1 "sigs.k8s.io/prow/pkg/client/informers/externalversions/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

const (
	JobStateQuerySourceBigQuery = "bigquery"
	JobStateQuerySourceCluster  = "cluster"
	// prowJobJobRunIDLabel is the label in prowJob for the prow job run ID. It is a unique identifier for job runs across different jobs
	prowJobJobRunIDLabel = "prow.k8s.io/build-id"
)

const (
	bigQueryLabelKeyApp                        = "client-application"
	bigQueryLabelKeyQuery                      = "query-details"
	bigQueryLabelValueApp                      = "aggregator"
	bigQueryLabelValueDisruptionRowCountByJob  = "disruption-row-count"
	bigQueryLabelValueDisruptionStats          = "aggregator-disruption-stats"
	bigQueryLabelValueJobRunFromName           = "aggregator-job-run-from-name"
	bigQueryLabelValueLastJobRunTime           = "aggregator-last-job-run-time"
	bigQueryLabelValueAggregatedTestRun        = "aggregator-aggregated-test-run"
	bigQueryLabelValueAlertHistoricalData      = "aggregator-alert-historical"
	bigQueryLabelValueAllJobs                  = "aggregator-all-jobs"
	bigQueryLabelValueAllJobsWithVariants      = "aggregator-all-jobs-with-variants"
	bigQueryLabelValueAllKnownAlerts           = "aggregator-all-known-alerts"
	bigQueryLabelValueDisruptionHistoricalData = "aggregator-disruption-historical"
	bigQueryLabelValueJobRunsSinceTime         = "aggregator-job-runs-since-time"
	bigQueryLabelValueAllReleases              = "aggregator-all-releases"
	bigQueryLabelValueReleaseTags              = "aggregator-release-tags"
	bigQueryLabelValueJobRunIDsSinceTime       = "aggregator-job-run-ids-since-time"
)

var (
	KnownQuerySources = sets.Set[string]{JobStateQuerySourceBigQuery: sets.Empty{}, JobStateQuerySourceCluster: sets.Empty{}}
)

type JobRunIdentifier struct {
	JobName  string
	JobRunID string
}

func GetStaticJobRunInfo(staticRunInfoJSON, staticRunInfoPath string) ([]JobRunIdentifier, error) {
	var jsonBytes []byte
	var jobRuns []JobRunIdentifier
	var err error
	if len(staticRunInfoJSON) == 0 {
		jsonBytes, err = os.ReadFile(staticRunInfoPath)
		if err != nil {
			return nil, err
		}
	} else {
		jsonBytes = []byte(staticRunInfoJSON)
	}

	if err = json.Unmarshal(jsonBytes, &jobRuns); err != nil {
		return nil, err
	}

	return jobRuns, nil
}

type JobRunGetter interface {
	// GetRelatedJobRuns gets all related job runs for analysis
	GetRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error)

	// GetRelatedJobRunsFromIdentifiers passes along minimal information known about the jobs already so that we can skip
	// querying and go directly to fetching the full job details when GetRelatedJobRuns is called
	GetRelatedJobRunsFromIdentifiers(ctx context.Context, jobRunIdentifiers []JobRunIdentifier) ([]jobrunaggregatorapi.JobRunInfo, error)
}

type JobRunWaiter interface {
	// Wait waits until all job runs finish, or time out
	Wait(ctx context.Context) ([]JobRunIdentifier, error)
}

// WaitUntilTime waits until readAt time has passed
func WaitUntilTime(ctx context.Context, readyAt time.Time) error {
	logrus.Infof("Waiting now=%v, ReadyAt=%v.\n", time.Now(), readyAt)

	if time.Now().After(readyAt) {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(readyAt)):
		break
	}
	logrus.Infof("finished waiting until %v", readyAt)
	return nil
}

func getAllFinishedJobRuns(ctx context.Context, relatedJobRuns []jobrunaggregatorapi.JobRunInfo) ([]jobrunaggregatorapi.JobRunInfo, []jobrunaggregatorapi.JobRunInfo, []string, []string) {
	finishedJobRuns := []jobrunaggregatorapi.JobRunInfo{}
	unfinishedJobRuns := []jobrunaggregatorapi.JobRunInfo{}
	finishedJobRunNames := []string{}
	unfinishedJobRunNames := []string{}

	if len(relatedJobRuns) == 0 {
		return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames
	}

	for i := range relatedJobRuns {
		jobRun := relatedJobRuns[i]
		if !jobRun.IsFinished(ctx) {
			logrus.Debugf("%v/%v is not finished", jobRun.GetJobName(), jobRun.GetJobRunID())
			unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
			unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
			continue
		}

		prowJob, err := jobRun.GetProwJob(ctx)
		if err != nil {
			logrus.WithError(err).Errorf("error reading prowjob %v", jobRun.GetJobRunID())
			unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
			unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
			continue
		}

		if prowJob.Status.CompletionTime == nil {
			logrus.Debugf("%v/%v has no completion time for resourceVersion=%v", jobRun.GetJobName(), jobRun.GetJobRunID(), prowJob.ResourceVersion)
			unfinishedJobRunNames = append(unfinishedJobRunNames, jobRun.GetJobRunID())
			unfinishedJobRuns = append(unfinishedJobRuns, jobRun)
			continue
		}
		finishedJobRuns = append(finishedJobRuns, jobRun)
		finishedJobRunNames = append(finishedJobRunNames, jobRun.GetJobName()+jobRun.GetJobRunID())
	}
	return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames
}

type BigQueryJobRunWaiter struct {
	JobRunGetter      JobRunGetter
	TimeToStopWaiting time.Time
}

func (w *BigQueryJobRunWaiter) Wait(ctx context.Context) ([]JobRunIdentifier, error) {
	clock := clock.RealClock{}
	relatedJobRuns, err := w.JobRunGetter.GetRelatedJobRuns(ctx)
	if err != nil {
		return nil, err
	}

	var finishedJobRuns, unfinishedJobRuns []jobrunaggregatorapi.JobRunInfo
	var unfinishedJobRunNames []string

	for {
		fmt.Println() // for prettier logs

		finishedJobRuns, unfinishedJobRuns, _, unfinishedJobRunNames = getAllFinishedJobRuns(ctx, relatedJobRuns)

		// ready or not, it's time to check
		if clock.Now().After(w.TimeToStopWaiting) {
			logrus.Infof("waited long enough. Ready or not, here I come. (readyOrNot=%v now=%v)", w.TimeToStopWaiting, clock.Now())
			break
		}

		if len(unfinishedJobRunNames) > 0 {
			logrus.Infof("found %d unfinished related jobRuns: %v\n", len(unfinishedJobRunNames), strings.Join(unfinishedJobRunNames, ", "))
			select {
			case <-time.After(10 * time.Minute):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		break
	}

	// Optional if we don't want to change the BigQuery path we can remove
	// This will save us from making an additional lookup immediately
	// after this call returns
	jobRunIdentifiers := make([]JobRunIdentifier, 0)
	for _, jobRunInfo := range finishedJobRuns {
		jobRunIdentifiers = append(jobRunIdentifiers, JobRunIdentifier{JobRunID: jobRunInfo.GetJobRunID(), JobName: jobRunInfo.GetJobName()})
	}

	for _, jobRunInfo := range unfinishedJobRuns {
		jobRunIdentifiers = append(jobRunIdentifiers, JobRunIdentifier{JobRunID: jobRunInfo.GetJobRunID(), JobName: jobRunInfo.GetJobName()})
	}

	return jobRunIdentifiers, nil
}

// ClusterJobRunWaiter implements a waiter that will wait for job completion based on live stats for prow jobs
// in the CI cluster.
// 1. It uses kube informers/cache mechanism to list all prowJob CRs
// 2. Filter out irrelevant prowJobs
// 3. Check if CompletionTime for prowJob Status is set.
// 4. If all jobs have CompletionTime set, wait is over. Otherwise, repeat above steps by polling.
//
// Polling only queries cache with no api-server interactions.
type ClusterJobRunWaiter struct {
	ProwJobClient      *prowjobclientset.Clientset
	TimeToStopWaiting  time.Time
	ProwJobMatcherFunc ProwJobMatcherFunc
}

func (w *ClusterJobRunWaiter) allProwJobsFinished(allItems []*prowv1.ProwJob) (bool, map[string]*prowv1.ProwJob) {
	uncompletedJobMap := map[string]*prowv1.ProwJob{}
	matchedJobMap := map[string]*prowv1.ProwJob{}

	for _, prowJob := range allItems {
		if !w.ProwJobMatcherFunc(prowJob) {
			continue
		}
		jobRunID := prowJob.Labels[prowJobJobRunIDLabel]
		matchedJobMap[jobRunID] = prowJob
		if prowJob.Status.CompletionTime != nil {
			continue
		}

		uncompletedJobMap[jobRunID] = prowJob
	}
	if len(uncompletedJobMap) == 0 {
		logrus.Info("all jobs completed")
		return true, matchedJobMap
	}
	logrus.Infof("%d/%d jobs completed, waiting for: [%v]", len(matchedJobMap)-len(uncompletedJobMap), len(matchedJobMap), strings.Join(sets.StringKeySet(uncompletedJobMap).List(), ", "))
	return false, matchedJobMap
}

func (w *ClusterJobRunWaiter) checkMatchedJobsForCompletion(prowJobInformer v1.ProwJobInformer) (bool, map[string]*prowv1.ProwJob, error) {
	allItems, err := prowJobInformer.Lister().List(labels.Everything())
	if err != nil {
		return false, nil, err
	}

	allDone, matchedJobs := w.allProwJobsFinished(allItems)
	return allDone, matchedJobs, nil
}

func (w *ClusterJobRunWaiter) Wait(ctx context.Context) ([]JobRunIdentifier, error) {
	if w.ProwJobClient == nil {
		return nil, fmt.Errorf("prowjob client is missing")
	}

	prowJobInformerFactory := prowjobinformers.NewSharedInformerFactoryWithOptions(w.ProwJobClient, 24*time.Hour, prowjobinformers.WithNamespace("ci"))
	prowJobInformer := prowJobInformerFactory.Prow().V1().ProwJobs()

	// done to be sure that the informer is shown as "active" so that start activates them
	// start informers and wait for them to sync
	hasSynced := prowJobInformer.Informer().HasSynced
	go prowJobInformerFactory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), hasSynced) {
		return nil, fmt.Errorf("prowjob informer sync error")
	}
	timeout := time.Until(w.TimeToStopWaiting)
	if timeout < 0 {
		timeout = 30 * time.Second
	}
	logrus.Infof("Going to wait until %+v with timeout value %+v", w.TimeToStopWaiting, timeout)

	// wait for up to limit until we've finished
	err := wait.PollUntilContextTimeout(
		ctx,
		5*time.Minute,
		timeout,
		true,
		func(ctx context.Context) (bool, error) {
			allDone, _, err := w.checkMatchedJobsForCompletion(prowJobInformer)

			if err != nil {
				// log and suppress the error
				logrus.Infof("Error listing prow jobs: %v", err)
				return false, nil
			}

			return allDone, err
		},
	)
	if err != nil && err != context.DeadlineExceeded {
		return nil, fmt.Errorf("failed waiting for prowjobs to complete: %w", err)
	}

	// one more time to get the matched jobs
	_, matchedJobs, err := w.checkMatchedJobsForCompletion(prowJobInformer)

	if err != nil && err != context.DeadlineExceeded {
		return nil, fmt.Errorf("failed waiting for prowjobs to complete: %w", err)
	}

	jobRuns := make([]JobRunIdentifier, len(matchedJobs))
	count := 0
	for _, value := range matchedJobs {
		jobRuns[count] = JobRunIdentifier{JobName: value.Spec.Job, JobRunID: value.Status.BuildID}
		count += 1
	}

	return jobRuns, nil
}

// WaitAndGetAllFinishedJobRuns waits for all job runs to finish until timeToStopWaiting. It returns all finished and unfinished job runs
func WaitAndGetAllFinishedJobRuns(ctx context.Context,
	jobRunGetter JobRunGetter,
	waiter JobRunWaiter,
	outputDir string,
	variantInfo string) ([]jobrunaggregatorapi.JobRunInfo, []jobrunaggregatorapi.JobRunInfo, []string, []string, error) {
	finishedJobRuns := []jobrunaggregatorapi.JobRunInfo{}
	unfinishedJobRuns := []jobrunaggregatorapi.JobRunInfo{}
	finishedJobRunNames := []string{}
	unfinishedJobRunNames := []string{}

	var err error
	matchedJobs, err := waiter.Wait(ctx)
	if err != nil {
		logrus.Errorf("finished waiting with error %+v", err)
		return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, err
	}
	logrus.Infof("finished waiting")

	var relatedJobRuns []jobrunaggregatorapi.JobRunInfo
	if len(matchedJobs) > 0 {
		relatedJobRuns, err = jobRunGetter.GetRelatedJobRunsFromIdentifiers(ctx, matchedJobs)
	} else {
		// Refresh the job runs content one last time
		relatedJobRuns, err = jobRunGetter.GetRelatedJobRuns(ctx)
	}
	if err != nil {
		return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, err
	}

	finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames = getAllFinishedJobRuns(ctx, relatedJobRuns)

	summaryHTML := htmlForJobRuns(ctx, finishedJobRuns, unfinishedJobRuns, variantInfo)
	if err := os.WriteFile(filepath.Join(outputDir, "job-run-summary.html"), []byte(summaryHTML), 0644); err != nil {
		return finishedJobRuns, unfinishedJobRuns, finishedJobRunNames, unfinishedJobRunNames, err
	}

	logrus.Infof("found %d finished jobRuns: %v and %d unfinished jobRuns: %v",
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

func GetProwJobClient() (*prowjobclientset.Clientset, error) {
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	clusterConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, err
	}

	prowJobClient, err := prowjobclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}
	return prowJobClient, nil
}
