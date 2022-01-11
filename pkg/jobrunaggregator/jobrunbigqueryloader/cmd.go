package jobrunbigqueryloader

import (
	"context"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryTestRunUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	DryRun bool
}

func NewBigQueryTestRunUploadFlags() *BigQueryTestRunUploadFlags {
	return &BigQueryTestRunUploadFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryTestRunUploadFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.BoolVar(&f.DryRun, "dry-run", f.DryRun, "Run the command, but don't mutate data.")
}

func NewBigQueryTestRunUploadFlagsCommand() *cobra.Command {
	f := NewBigQueryTestRunUploadFlags()

	cmd := &cobra.Command{
		Use:          "upload-test-runs",
		Long:         `Upload test runs to bigquery`,
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
func (f *BigQueryTestRunUploadFlags) Validate() error {
	if err := f.DataCoordinates.Validate(); err != nil {
		return err
	}
	if err := f.Authentication.Validate(); err != nil {
		return err
	}

	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *BigQueryTestRunUploadFlags) ToOptions(ctx context.Context) (*allJobsLoaderOptions, error) {
	// Create a new GCS Client
	gcsClient, err := f.Authentication.NewCIGCSClient(ctx, "origin-ci-test")
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

	var jobRunTableInserter jobrunaggregatorlib.BigQueryInserter
	var testRunTableInserter jobrunaggregatorlib.BigQueryInserter
	if !f.DryRun {
		ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)
		jobRunTable := ciDataSet.Table(jobrunaggregatorapi.LegacyJobRunTableName)
		testRunTable := ciDataSet.Table(jobrunaggregatorlib.TestRunTableName)
		jobRunTableInserter = jobRunTable.Inserter()
		testRunTableInserter = testRunTable.Inserter()
	} else {
		jobRunTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorapi.LegacyJobRunTableName)
		testRunTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorlib.TestRunTableName)
	}

	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter:              jobRunTableInserter,
		shouldCollectedDataForJobFn: wantsTestRunData,
		getLastJobRunWithDataFn:     ciDataClient.GetLastJobRunWithTestRunDataForJobName,
		jobRunUploader:              newTestRunUploader(testRunTableInserter),
	}, nil
}
