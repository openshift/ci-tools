package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/clock"

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
	AggrDataTime                string
}

func NewJobRunsAnalyzerFlags() *JobRunsAnalyzerFlags {
	return &JobRunsAnalyzerFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),

		WorkingDir:                  "job-aggregator-working-dir",
		EstimatedJobStartTimeString: time.Now().Format(kubeTimeSerializationLayout),
		Timeout:                     3*time.Hour + 30*time.Minute,
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
	fs.StringVar(&f.EstimatedJobStartTimeString, "job-start-time", f.EstimatedJobStartTimeString, fmt.Sprintf("Start time in RFC3399: %s", kubeTimeSerializationLayout))
	fs.StringVar(&f.AggrDataTime, "aggr-data-start-time", f.AggrDataTime, fmt.Sprintf("Aggregation data start time (used to target specific aggregation data) in RFC3399: %s", kubeTimeSerializationLayout))
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
	if len(f.AggrDataTime) > 0 {
		_, err := time.Parse(kubeTimeSerializationLayout, f.AggrDataTime)
		if err != nil {
			return errors.Wrap(err, "The --aggr-data-start-time value needs to be a valid time")
		}
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
	var aggrDataTime time.Time
	if len(f.AggrDataTime) == 0 {
		aggrDataTime = estimatedStartTime
	} else {
		aggrDataTime, err = time.Parse(kubeTimeSerializationLayout, f.AggrDataTime)
		if err != nil {
			return nil, err
		}
	}
	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)

	gcsClient, err := f.Authentication.NewGCSClient(ctx)
	if err != nil {
		return nil, err
	}
	ciGCSClient, err := f.Authentication.NewCIGCSClient(ctx, "origin-ci-test")
	if err != nil {
		return nil, err
	}

	var jobRunLocator jobrunaggregatorlib.JobRunLocator
	if len(f.PayloadTag) > 0 {
		jobRunLocator = jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
			f.JobName,
			f.PayloadTag,
			estimatedStartTime,
			ciDataClient,
			ciGCSClient,
			gcsClient,
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
			gcsClient,
			"origin-ci-test",
			f.ExplicitGCSPrefix,
		)
	}

	return &JobRunAggregatorAnalyzerOptions{
		explicitGCSPrefix:   f.ExplicitGCSPrefix,
		jobRunLocator:       jobRunLocator,
		passFailCalculator:  newWeeklyAverageFromTenDaysAgo(f.JobName, estimatedStartTime, aggrDataTime, 3, ciDataClient),
		jobName:             f.JobName,
		payloadTag:          f.PayloadTag,
		workingDir:          f.WorkingDir,
		jobRunStartEstimate: estimatedStartTime,
		clock:               clock.RealClock{},
		timeout:             f.Timeout,
	}, nil
}
