package jobrunhistoricaldataanalyzer

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type JobRunHistoricalDataAnalyzerFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	NewFile         string
	CurrentFile     string
	DataType        string
	Leeway          float64
	OutputFile      string
	TargetRelease   string
	PreviousRelease string
}

var supportedDataTypes = sets.New[string]("alerts", "disruptions", "tests")

func NewJobRunHistoricalDataAnalyzerFlags() *JobRunHistoricalDataAnalyzerFlags {
	return &JobRunHistoricalDataAnalyzerFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *JobRunHistoricalDataAnalyzerFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.StringVar(&f.DataType, "data-type", f.DataType, fmt.Sprintf("data type we are fetching %s", sets.List(supportedDataTypes)))
	fs.StringVar(&f.NewFile, "new", f.NewFile, "local file with the new query results to compare against")
	fs.StringVar(&f.CurrentFile, "current", f.CurrentFile, "local file with the current query results")
	fs.StringVar(&f.OutputFile, "output-file", f.OutputFile, "output file for the resulting comparison results")
	fs.StringVar(&f.TargetRelease, "target-release", f.TargetRelease, "override for release to generate data for, omit to use the most recent release. Be sure to checkout the correct branch for --current.")
	fs.StringVar(&f.PreviousRelease, "previous-release", f.PreviousRelease, "override for previous release to generate data when we do not have enough for target release. Must be specified if using --target-release.")
	fs.Float64Var(&f.Leeway, "leeway", f.Leeway, "percent leeway threshold for increased time diff")
}

func (f *JobRunHistoricalDataAnalyzerFlags) Validate() error {
	if err := f.DataCoordinates.Validate(); err != nil && f.NewFile == "" {
		return err
	}
	if err := f.Authentication.Validate(); err != nil && f.NewFile == "" {
		return err
	}

	if !supportedDataTypes.Has(f.DataType) {
		return fmt.Errorf("must provide supported datatype %v", sets.List(supportedDataTypes))
	}

	// For tests data type, we don't need --current since we don't do comparison
	if f.DataType != "tests" && f.CurrentFile == "" {
		return fmt.Errorf("must provide --current [file_path] flag to compare against")
	}

	if f.Leeway < 0 {
		return fmt.Errorf("leeway percent must be above 0")
	}

	if f.TargetRelease != "" && f.PreviousRelease == "" && f.DataType != "tests" {
		return fmt.Errorf("must specify --previous-release with --target-release")
	}

	return nil
}

func (f *JobRunHistoricalDataAnalyzerFlags) ToOptions(ctx context.Context) (*JobRunHistoricalDataAnalyzerOptions, error) {
	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil && f.NewFile == "" {
		return nil, err
	}

	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)

	if f.OutputFile == "" {
		f.OutputFile = fmt.Sprintf("results_%s.json", f.DataType)
	}

	return &JobRunHistoricalDataAnalyzerOptions{
		ciDataClient:    ciDataClient,
		newFile:         f.NewFile,
		currentFile:     f.CurrentFile,
		leeway:          f.Leeway,
		dataType:        f.DataType,
		outputFile:      f.OutputFile,
		targetRelease:   f.TargetRelease,
		previousRelease: f.PreviousRelease,
	}, nil
}

func NewJobRunHistoricalDataAnalyzerCommand() *cobra.Command {
	f := NewJobRunHistoricalDataAnalyzerFlags()

	cmd := &cobra.Command{
		Use:          "analyze-historical-data",
		Short:        `Upload release data to bigquery`,
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
