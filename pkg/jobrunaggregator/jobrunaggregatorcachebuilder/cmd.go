package jobrunaggregatorcachebuilder

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/api/option"
)

const (
	openshiftCIBucket string = "origin-ci-test"
)

type JobRunsAggregatorCacheBuilderFlags struct {
	JobNames      []string
	WorkingDir    string
	GCSBucketName string
}

func NewJobRunsAggregatorCacheBuilderFlags() *JobRunsAggregatorCacheBuilderFlags {
	return &JobRunsAggregatorCacheBuilderFlags{
		WorkingDir:    "job-aggregator-working-dir",
		GCSBucketName: "origin-ci-test",
	}
}

func (f *JobRunsAggregatorCacheBuilderFlags) BindFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&f.JobNames, "jobs", f.JobNames, "The name of the jobs to inspect, like periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade")
	fs.StringVar(&f.WorkingDir, "working-dir", f.WorkingDir, "The directory to store caches, output, and the like.")
	fs.StringVar(&f.GCSBucketName, "gcs-bucket-name", f.GCSBucketName, "GCS bucket to read from.")
}

func NewJobRunsAggregatorCacheBuilderCommand() *cobra.Command {
	f := NewJobRunsAggregatorCacheBuilderFlags()

	cmd := &cobra.Command{
		Use:          "cache-job-runs",
		Long:         `Cache CI jobs runs locally for analysis`,
		Deprecated:   "No longer used. This exists to make it convenient to pull data locally if anyone ever wants to inspect it.",
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
func (f *JobRunsAggregatorCacheBuilderFlags) Validate() error {
	if len(f.WorkingDir) == 0 {
		return fmt.Errorf("missing --working-dir: like job-aggregator-working-dir")
	}
	if len(f.JobNames) == 0 {
		return fmt.Errorf("missing --jobs: like periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade")
	}

	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *JobRunsAggregatorCacheBuilderFlags) ToOptions(ctx context.Context) (*JobRunsAggregatorCacheBuilderOptions, error) {
	// Create a new GCS Client
	gcsClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		return nil, err
	}

	return &JobRunsAggregatorCacheBuilderOptions{
		JobNames:   f.JobNames,
		GCSClient:  gcsClient,
		WorkingDir: f.WorkingDir,
	}, nil
}
