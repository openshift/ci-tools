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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/clock"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobclientset "sigs.k8s.io/prow/pkg/client/clientset/versioned"
	prowjoblisters "sigs.k8s.io/prow/pkg/client/listers/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/junit"
)

const (
	JobStateQuerySourceBigQuery = "bigquery"
	JobStateQuerySourceCluster  = "cluster"
	// prowJobJobRunIDLabel is the label in prowJob for the prow job run ID. It is a unique identifier for job runs across different jobs
	prowJobJobRunIDLabel = "prow.k8s.io/build-id"
	// prowJobNamespace is the namespace holding the ProwJobs of the CI cluster
	prowJobNamespace = "ci"
	// prowJobListPageSize bounds how many untrimmed ProwJobs are decoded at once when the
	// prowjob informer does its initial list. The namespace holds tens of thousands of
	// ProwJobs, so this trades a few hundred megabytes of peak usage against the number of
	// requests the initial sync needs.
	prowJobListPageSize = 5000
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
	bigQueryLabelValueTestSummaryByPeriod      = "aggregator-test-summary-by-period"
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

// trimProwJob drops the parts of a ProwJob that no aggregator ever reads, so that the informer
// cache holds job names, labels, annotations and completion state instead of whole pod specs.
// The ProwJob is trimmed in place: the caller always owns the only reference to it.
//
// This is the same idea as pjutil.TrimCachedProwJob upstream (kubernetes-sigs/prow#904), which
// several prow components share. Once that merges and we pick it up in a prow bump, most of the
// body below can defer to it, but not all of it: that helper keeps ExtraRefs because the
// exporter builds metric labels out of it, and keeps the status fields that deck and tide serve,
// while nothing here reads any of them.
func trimProwJob(prowJob *prowv1.ProwJob) {
	prowJob.ManagedFields = nil
	prowJob.OwnerReferences = nil
	prowJob.Spec.PodSpec = nil
	prowJob.Spec.PipelineRunSpec = nil
	prowJob.Spec.TektonPipelineRunSpec = nil
	prowJob.Spec.DecorationConfig = nil
	prowJob.Spec.ExtraRefs = nil
	prowJob.Spec.RerunAuthConfig = nil
	prowJob.Spec.RerunCommand = ""
	prowJob.Status.Description = ""
	prowJob.Status.PrevReportStates = nil
}

// trimProwJobObject is the cache.TransformFunc form of trimProwJob
func trimProwJobObject(obj interface{}) (interface{}, error) {
	switch typed := obj.(type) {
	case *prowv1.ProwJob:
		trimProwJob(typed)
	case cache.DeletedFinalStateUnknown:
		if prowJob, ok := typed.Obj.(*prowv1.ProwJob); ok {
			trimProwJob(prowJob)
		}
	}
	return obj, nil
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

func (w *ClusterJobRunWaiter) checkMatchedJobsForCompletion(prowJobLister prowjoblisters.ProwJobLister) (bool, map[string]*prowv1.ProwJob, error) {
	allItems, err := prowJobLister.List(labels.Everything())
	if err != nil {
		return false, nil, err
	}

	allDone, matchedJobs := w.allProwJobsFinished(allItems)
	return allDone, matchedJobs, nil
}

// newProwJobInformer builds an informer over all ProwJobs in the CI cluster, tuned to keep the
// aggregator's memory footprint down. A ProwJob embeds the full pod spec of the job it runs,
// which for ci-operator jobs carries the unresolved ci-operator configuration and is by far the
// largest part of the object, and the CI cluster holds tens of thousands of ProwJobs at any
// time. Neither the list nor the cache can therefore hold whole ProwJobs:
//   - the initial list is paged by hand and each page is trimmed before it is accumulated, so at
//     most one page worth of untrimmed ProwJobs is ever decoded at once. The generated informers
//     cannot do this: the reflector accumulates every page of its initial list into one slice
//     before the transform below ever runs, which happens later, in the DeltaFIFO. It does ask
//     the server to paginate, but a resourceVersion=0 list is served from the watch cache, which
//     ignores the limit and returns the whole collection in one response anyway.
//   - a transform trims the objects the watch delivers afterwards.
//
// The streaming watch-list initial sync (SendInitialEvents) would make the hand-rolled paging
// unnecessary, because from client-go 1.34 the reflector trims the objects it streams into its
// internal store. It is of no use to us yet: the client-side WatchListClient gate only defaults
// to true in client-go 1.35 (we build against 1.33), and, more importantly, the server-side
// WatchList gate is alpha and off in Kubernetes 1.31, which is what the CI cluster (OCP 4.18)
// runs. Upstream also turned that gate back off by default in 1.33 after it was briefly on in
// 1.32, so a newer cluster would not obviously help either. When the server honors
// SendInitialEvents, the reflector uses WatchFunc below and ignores ListFunc, which can then go.
func (w *ClusterJobRunWaiter) newProwJobInformer(ctx context.Context) (cache.SharedIndexInformer, error) {
	prowJobs := w.ProwJobClient.ProwV1().ProwJobs(prowJobNamespace)
	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			// page ourselves, which requires a quorum read: the watch cache cannot paginate
			options.ResourceVersion = ""
			options.ResourceVersionMatch = ""
			options.Continue = ""
			options.Limit = prowJobListPageSize
			trimmed := &prowv1.ProwJobList{}
			for pageNumber := 1; ; pageNumber++ {
				page, err := prowJobs.List(ctx, options)
				if err != nil {
					return nil, fmt.Errorf("failed to list prowjobs in namespace %q (page %d): %w", prowJobNamespace, pageNumber, err)
				}
				if pageNumber == 1 {
					// every page of a paged list is served from the same snapshot, so the
					// resourceVersion the watch resumes from is the one of the first page
					trimmed.TypeMeta = page.TypeMeta
					trimmed.ResourceVersion = page.ResourceVersion
				}
				for i := range page.Items {
					trimProwJob(&page.Items[i])
					trimmed.Items = append(trimmed.Items, page.Items[i])
				}
				if len(page.Continue) == 0 {
					// no Continue on the returned list: the reflector's pager must not ask for more
					return trimmed, nil
				}
				options.Continue = page.Continue
			}
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return prowJobs.Watch(ctx, options)
		},
	}

	informer := cache.NewSharedIndexInformer(listWatch, &prowv1.ProwJob{}, 24*time.Hour, cache.Indexers{})
	if err := informer.SetTransform(trimProwJobObject); err != nil {
		return nil, fmt.Errorf("failed to set prowjob informer transform: %w", err)
	}
	return informer, nil
}

func (w *ClusterJobRunWaiter) Wait(ctx context.Context) ([]JobRunIdentifier, error) {
	if w.ProwJobClient == nil {
		return nil, fmt.Errorf("prowjob client is missing")
	}

	// the cached prowjobs are only needed while we wait, so shut the informer down on the way out
	// instead of keeping the cache around for the rest of the analysis
	informerCtx, shutdownInformer := context.WithCancel(ctx)
	defer shutdownInformer()

	prowJobInformer, err := w.newProwJobInformer(informerCtx)
	if err != nil {
		return nil, err
	}

	// start the informer and wait for it to sync
	go prowJobInformer.Run(informerCtx.Done())
	if !cache.WaitForCacheSync(informerCtx.Done(), prowJobInformer.HasSynced) {
		return nil, fmt.Errorf("prowjob informer sync error")
	}
	prowJobLister := prowjoblisters.NewProwJobLister(prowJobInformer.GetIndexer())
	timeout := time.Until(w.TimeToStopWaiting)
	if timeout < 0 {
		timeout = 30 * time.Second
	}
	logrus.Infof("Going to wait until %+v with timeout value %+v", w.TimeToStopWaiting, timeout)

	// wait for up to limit until we've finished
	err = wait.PollUntilContextTimeout(
		ctx,
		5*time.Minute,
		timeout,
		true,
		func(ctx context.Context) (bool, error) {
			allDone, _, err := w.checkMatchedJobsForCompletion(prowJobLister)

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
	_, matchedJobs, err := w.checkMatchedJobsForCompletion(prowJobLister)

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
