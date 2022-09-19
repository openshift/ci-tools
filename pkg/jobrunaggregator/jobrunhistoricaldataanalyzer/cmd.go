package jobrunhistoricaldataanalyzer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
)

type JobRunHistoricalDataAnalyzerFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	NewFile        string
	CurrentFile    string
	DataType       string
	Leeway         string
	OutputFile     string
	DurationLeeway time.Duration
}

var supportedDataTypes = sets.NewString("alerts", "disruptions")

func NewJobRunHistoricalDataAnalyzerFlags() *JobRunHistoricalDataAnalyzerFlags {
	return &JobRunHistoricalDataAnalyzerFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *JobRunHistoricalDataAnalyzerFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.StringVar(&f.DataType, "data-type", f.DataType, "data type to use")
	fs.StringVar(&f.NewFile, "new", f.NewFile, "new file")
	fs.StringVar(&f.CurrentFile, "current", f.CurrentFile, "old file")
	fs.StringVar(&f.Leeway, "leeway", f.DurationLeeway.String(), "time leeway")
	fs.StringVar(&f.OutputFile, "output-file", f.OutputFile, "save merge output, add missing json data")
}

func (f *JobRunHistoricalDataAnalyzerFlags) Validate() error {
	if err := f.DataCoordinates.Validate(); err != nil && f.NewFile == "" {
		return err
	}
	if err := f.Authentication.Validate(); err != nil && f.NewFile == "" {
		return err
	}

	if !supportedDataTypes.Has(strings.ToLower(f.DataType)) {
		return fmt.Errorf("must provide supported datatype %v", supportedDataTypes.List())
	}

	if f.CurrentFile == "" {
		return fmt.Errorf("must provide --current [file_path] flag to compare against")
	}

	leeway, err := time.ParseDuration(f.Leeway)
	if err != nil {
		return err
	}
	f.DurationLeeway = leeway

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

	return &JobRunHistoricalDataAnalyzerOptions{
		ciDataClient: ciDataClient,
		newFile:      f.NewFile,
		currentFile:  f.CurrentFile,
		leeway:       f.DurationLeeway,
		dataType:     f.DataType,
		outputFile:   f.OutputFile,
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
