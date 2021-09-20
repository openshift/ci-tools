package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"

	"cloud.google.com/go/bigquery"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type BigQueryTestRunUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
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

	client, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, client)
	ciDataSet := client.Dataset(f.DataCoordinates.DataSetID)
	jobRunTable := ciDataSet.Table(jobrunaggregatorlib.JobRunTableName)
	testRunTable := ciDataSet.Table(jobrunaggregatorlib.TestRunTableName)

	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter:              jobRunTable.Inserter(),
		shouldCollectedDataForJobFn: wantsTestRunData,
		getLastJobRunWithDataFn:     ciDataClient.GetLastJobRunWithTestRunDataForJobName,
		jobRunUploader:              newTestRunUploader(testRunTable.Inserter()),
	}, nil
}

type BigQueryDisruptionUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
}

func NewBigQueryDisruptionUploadFlags() *BigQueryDisruptionUploadFlags {
	return &BigQueryDisruptionUploadFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryDisruptionUploadFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
}

func NewBigQueryDisruptionUploadFlagsCommand() *cobra.Command {
	f := NewBigQueryDisruptionUploadFlags()

	cmd := &cobra.Command{
		Use:          "upload-disruptions",
		Long:         `Upload disruption data to bigquery`,
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
func (f *BigQueryDisruptionUploadFlags) Validate() error {
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
func (f *BigQueryDisruptionUploadFlags) ToOptions(ctx context.Context) (*allJobsLoaderOptions, error) {
	// Create a new GCS Client
	gcsClient, err := f.Authentication.NewCIGCSClient(ctx, "origin-ci-test")
	if err != nil {
		return nil, err
	}

	client, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, client)
	ciDataSet := client.Dataset(f.DataCoordinates.DataSetID)
	jobRunTable := ciDataSet.Table(jobrunaggregatorlib.JobRunTableName)
	backendDisruptionTable := ciDataSet.Table(jobrunaggregatorapi.BackendDisruptionTableName)

	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter:              jobRunTable.Inserter(),
		shouldCollectedDataForJobFn: wantsDisruptionData,
		getLastJobRunWithDataFn:     ciDataClient.GetLastJobRunWithDisruptionDataForJobName,
		jobRunUploader:              newDisruptionUploader(backendDisruptionTable.Inserter()),
	}, nil
}

type BigQuerySummarizationFlags struct {
	SummaryTimeFrame string

	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
}

func NewBigQuerySummarizationFlags() *BigQuerySummarizationFlags {
	return &BigQuerySummarizationFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQuerySummarizationFlags) BindFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.SummaryTimeFrame, "summary-timeframe", f.SummaryTimeFrame, "summary timeframe")
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
}

func NewBigQuerySummarizationFlagsCommand() *cobra.Command {
	f := NewBigQuerySummarizationFlags()

	cmd := &cobra.Command{
		Use:          "summarize-test-runs",
		Long:         `Summarize test runs in bigquery`,
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
func (f *BigQuerySummarizationFlags) Validate() error {
	switch f.SummaryTimeFrame {
	case "ByOneDay":
	case "ByOneWeek":
	default:
		return fmt.Errorf("invalid summary timeframe: %q", f.SummaryTimeFrame)
	}

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
func (f *BigQuerySummarizationFlags) ToOptions(ctx context.Context) (*JobRunsBigQuerySummarizerOptions, error) {
	client, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataSet := client.Dataset(f.DataCoordinates.DataSetID)

	var summarizedTestRunTable *bigquery.Table
	summaryDuration := time.Duration(1) * 24 * time.Hour
	switch f.SummaryTimeFrame {
	case "ByOneDay":
		summaryDuration = time.Duration(1) * 24 * time.Hour
		summarizedTestRunTable = ciDataSet.Table(jobrunaggregatorlib.PerDayTestRunTable)
	case "ByOneWeek":
		summaryDuration = time.Duration(1) * 7 * 24 * time.Hour
		summarizedTestRunTable = ciDataSet.Table(jobrunaggregatorlib.PerWeekTestRunTable)
	default:
		return nil, fmt.Errorf("invalid summary timeframe: %q", f.SummaryTimeFrame)
	}

	return &JobRunsBigQuerySummarizerOptions{
		Frequency:                 f.SummaryTimeFrame,
		SummaryDuration:           summaryDuration,
		CIDataClient:              jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, client),
		DataCoordinates:           f.DataCoordinates,
		AggregatedTestRunInserter: summarizedTestRunTable.Inserter(),
	}, nil
}
