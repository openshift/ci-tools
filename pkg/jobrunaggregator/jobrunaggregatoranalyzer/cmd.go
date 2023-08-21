package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/client-go/tools/clientcmd"
	prowjobclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	"k8s.io/utils/clock"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunsAnalyzerFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	JobName                     string
	WorkingDir                  string
	PayloadTag                  string
	AggregationID               string
	ExplicitGCSPrefix           string
	Timeout                     time.Duration
	EstimatedJobStartTimeString string
	QuerySource                 string
}

const (
	bigQuerySource = "big-query"
	payloadLabel = "release.openshift.io/payload"
)

func NewJobRunsAnalyzerFlags() *JobRunsAnalyzerFlags {
	return &JobRunsAnalyzerFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),

		WorkingDir:                  "job-aggregator-working-dir",
		EstimatedJobStartTimeString: time.Now().Format(kubeTimeSerializationLayout),
		Timeout:                     5*time.Hour + 30*time.Minute,
	}
}

const kubeTimeSerializationLayout = time.RFC3339

func (f *JobRunsAnalyzerFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.StringVar(&f.JobName, "job", f.JobName, "The name of the job to inspect, like periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade")
	fs.StringVar(&f.WorkingDir, "working-dir", f.WorkingDir, "The directory to store caches, output, and the like.")
	fs.StringVar(&f.PayloadTag, "payload-tag", f.PayloadTag, "The payload tag to aggregate, like 4.9.0-0.ci-2021-07-19-185802")
	fs.StringVar(&f.AggregationID, "aggregation-id", f.AggregationID, "mutually exclusive to --payload-tag.  Matches the .label[release.openshift.io/aggregation-id] on the prowjob, which is a UID")
	fs.StringVar(&f.ExplicitGCSPrefix, "explicit-gcs-prefix", f.ExplicitGCSPrefix, "only used by per PR payload promotion jobs.  This overrides the well-known mapping and becomes the required prefix for the GCS query")
	fs.DurationVar(&f.Timeout, "timeout", f.Timeout, "Time to wait for aggregation to complete.")
	fs.StringVar(&f.EstimatedJobStartTimeString, "job-start-time", f.EstimatedJobStartTimeString, fmt.Sprintf("Start time in RFC822Z: %s", kubeTimeSerializationLayout))
	fs.StringVar(&f.QuerySource, "query-source", bigQuerySource, "The source from which jobs are found. It is either big-query or cluster")
}

func NewJobRunsAnalyzerCommand() *cobra.Command {
	f := NewJobRunsAnalyzerFlags()

	cmd := &cobra.Command{
		Use:          "analyze-job-runs",
		Long:         `Aggregate job runs, determine pass/fail counts for every test, decide if the average is an overall pass or fail.`,
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := f.Validate(); err != nil {
				logrus.WithError(err).Fatal("Flags are invalid")
			}
			o, err := f.ToOptions(ctx)
			if err != nil {
				logrus.WithError(err).Fatal("Failed to build runtime options")
			}

			if err := o.Run(ctx); err != nil {
				logrus.WithError(err).Fatal("Command failed")
			}

			return nil
		},

		Args: jobrunaggregatorlib.NoArgs,
	}

	f.BindFlags(cmd.Flags())

	return cmd
}

// Validate checks to see if the user-input is likely to produce functional runtime options
func (f *JobRunsAnalyzerFlags) Validate() error {
	if len(f.WorkingDir) == 0 {
		return fmt.Errorf("missing --working-dir: like job-aggregator-working-dir")
	}
	if len(f.JobName) == 0 {
		return fmt.Errorf("missing --job: like periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade")
	}
	if _, err := time.Parse(kubeTimeSerializationLayout, f.EstimatedJobStartTimeString); err != nil {
		return err
	}
	if err := f.DataCoordinates.Validate(); err != nil {
		return err
	}
	if err := f.Authentication.Validate(); err != nil {
		return err
	}
	if len(f.PayloadTag) > 0 && len(f.AggregationID) > 0 {
		return fmt.Errorf("cannot specify both --payload-tag and --aggregation-id")
	}
	if len(f.PayloadTag) == 0 && len(f.AggregationID) == 0 {
		return fmt.Errorf("exactly one of --payload-tag or --aggregation-id must be specified")
	}
	if len(f.AggregationID) > 0 && len(f.ExplicitGCSPrefix) == 0 {
		return fmt.Errorf("if --aggregation-id is specified, you must specify --explicit-gcs-prefix")
	}

	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *JobRunsAnalyzerFlags) ToOptions(ctx context.Context) (*JobRunAggregatorAnalyzerOptions, error) {
	estimatedStartTime, err := time.Parse(kubeTimeSerializationLayout, f.EstimatedJobStartTimeString)
	if err != nil {
		return nil, err
	}

	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)

	ciGCSClient, err := f.Authentication.NewCIGCSClient(ctx, "origin-ci-test")
	if err != nil {
		return nil, err
	}

	var prowJobClient *prowjobclientset.Clientset
	if f.QuerySource != bigQuerySource {
		cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
		clusterConfig, err := cfg.ClientConfig()
		if err != nil {
			return nil, err
		}
		fmt.Printf("config: %+v\n", clusterConfig)

		prowJobClient, err = prowjobclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, err
		}

		//list, err := prowJobClient.ProwV1().ProwJobs("ci").List(ctx, metav1.ListOptions{})
		//fmt.Printf("err %+v, list: %+v\n", err, list)

		/*prowJobInformerFactory := prowjobinformers.NewSharedInformerFactory(prowJobClient, 24 * time.Hour)
		prowJobInformer := prowJobInformerFactory.Prow().V1().ProwJobs()

		prowJobInformer.Informer().AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					prowJob := obj.(*prowv1.ProwJob)
					fmt.Printf("AddFunc for nanmespace: %s, object: %s\n", prowJob.GetNamespace(), prowJob.GetName())
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
					oldProwJob := oldObj.(*prowv1.ProwJob)
					newProwJob := newObj.(*prowv1.ProwJob)
					fmt.Printf("UpdateFunc for nanmespace: %s, object: %s, new ns: %s, new name: %s\n",
						oldProwJob.GetNamespace(), oldProwJob.GetName(),
						newProwJob.GetNamespace(), newProwJob.GetName())
				},
				DeleteFunc: func(obj interface{}) {
					prowJob := obj.(*prowv1.ProwJob)
					fmt.Printf("DeleteFunc for nanmespace: %s, object: %s\n", prowJob.GetNamespace(), prowJob.GetName())
				},
			},
		)
		prowJobInformerFactory.Start(ctx.Done())
		if !cache.WaitForCacheSync(ctx.Done(), prowJobInformer.Informer().HasSynced) {
			return nil, fmt.Errorf("prowjob informer sync error")
		}
		fmt.Printf("cache syned\n")

		prowJobLister := prowJobInformer.Lister()
		var requiredLabel *labels.Requirement
		if len(f.PayloadTag) > 0 {
			requiredLabel, err = labels.NewRequirement(payloadLabel, selection.Equals, []string{f.PayloadTag})
			if err != nil {
				return nil, err
			}
		} else {
			requiredLabel, err = labels.NewRequirement(v1.PullRequestPayloadQualificationRunLabel, selection.Equals, []string{f.AggregationID})
			if err != nil {
				return nil, err
			}
		}
		prowjobs, err := prowJobLister.ProwJobs("ci").List(labels.NewSelector().Add(*requiredLabel))
		//prowjobs, err = prowJobLister.ProwJobs("ci").List(labels.Everything())
		if err != nil {
			return nil, err
		}
		for _, prowJob := range prowjobs {
			fmt.Printf("prowJob: %+v\n", prowJob)
		}

		return nil, fmt.Errorf("stopped for now")*/



		/*dynamicClient, err := dynamic.NewForConfig(clusterConfig)
		if err != nil {
			return nil, err
		}
		dynamicInformer := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)
		prowJobResource := schema.GroupVersionResource{
			Group:    "prow.k8s.io",
			Version:  "v1",
			Resource: "ProwJob",
		}
		prowJobInformer := dynamicInformer.ForResource(prowJobResource).Informer()
		if err != nil {
			return nil, err
		}
		dynamicInformer.Start(ctx.Done())
		prowJobInformer.AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
				},
				UpdateFunc: func(oldObj, newObj interface{}) {
				},
				DeleteFunc: func(obj interface{}) {
				},
			},
		)

		prowJobLister := dynamicInformer.ForResource(prowJobResource).Lister()
		var requiredLabel *labels.Requirement
		if len(f.PayloadTag) > 0 {
			requiredLabel, err = labels.NewRequirement(payloadLabel, selection.Equals, []string{f.PayloadTag})
			if err != nil {
				return nil, err
			}
		} else {
			requiredLabel, err = labels.NewRequirement(v1.PullRequestPayloadQualificationRunLabel, selection.Equals, []string{f.AggregationID})
			if err != nil {
				return nil, err
			}
		}
		objs, err := prowJobLister.ByNamespace("ci").List(labels.NewSelector().Add(*requiredLabel))
		if err != nil {
			return nil, err
		}
		if len(objs) == 0 {
			return nil, fmt.Errorf("no HostedControlPlane found in namespace")
		}

		var prowJobs []*prowv1.ProwJob
		for _, obj := range objs {
			unstructuredObj, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("can't convert")
			}

			prowJob := &prowv1.ProwJob{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.UnstructuredContent(), prowJob)
			if err != nil {
				return nil, err
			}
			prowJobs = append(prowJobs, prowJob)
		}*/


	}

	var jobRunLocator jobrunaggregatorlib.JobRunLocator
	if len(f.PayloadTag) > 0 {
		jobRunLocator = jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
			f.JobName,
			f.PayloadTag,
			estimatedStartTime,
			ciDataClient,
			ciGCSClient,
			"origin-ci-test",
		)
	}
	if len(f.AggregationID) > 0 {
		jobRunLocator = jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForPR(
			f.JobName,
			f.AggregationID,
			jobrunaggregatorlib.AggregationIDLabel,
			estimatedStartTime,
			ciDataClient,
			ciGCSClient,
			"origin-ci-test",
			f.ExplicitGCSPrefix,
		)
	}


	return &JobRunAggregatorAnalyzerOptions{
		explicitGCSPrefix:   f.ExplicitGCSPrefix,
		jobRunLocator:       jobRunLocator,
		passFailCalculator:  newWeeklyAverageFromTenDaysAgo(f.JobName, estimatedStartTime, 6, ciDataClient),
		jobName:             f.JobName,
		payloadTag:          f.PayloadTag,
		aggregationID: f.AggregationID,
		workingDir:          f.WorkingDir,
		jobRunStartEstimate: estimatedStartTime,
		clock:               clock.RealClock{},
		timeout:             f.Timeout,
		prowJobClient: prowJobClient,
		querySource: f.QuerySource,
	}, nil
}
