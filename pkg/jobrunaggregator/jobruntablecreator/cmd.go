package jobruntablecreator

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryJobRunTableCreateFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
}

func NewBigQueryJobRunTableCreateFlags() *BigQueryJobRunTableCreateFlags {
	return &BigQueryJobRunTableCreateFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryJobRunTableCreateFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
}

func NewBigQueryJobRunTableCreateFlagsCommand() *cobra.Command {
	f := NewBigQueryJobRunTableCreateFlags()

	cmd := &cobra.Command{
		Use:          "create-tables",
		Short:        "Create Jobs table in bigquery",
		Long:         "Create Jobs table in bigquery",
		SilenceUsage: false,

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
func (f *BigQueryJobRunTableCreateFlags) Validate() error {
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
func (f *BigQueryJobRunTableCreateFlags) ToOptions(ctx context.Context) (*allJobRunTableCreatorOptions, error) {
	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)
	ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)

	return &allJobRunTableCreatorOptions{
		ciDataClient: ciDataClient,
		ciDataSet:    ciDataSet,
	}, nil
}
