package releasebigqueryloader

import (
	"context"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryReleaseUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
	Releases        []string
	Architectures   []string
}

func NewBigQueryReleaseUploadFlags() *BigQueryReleaseUploadFlags {
	return &BigQueryReleaseUploadFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryReleaseUploadFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
	fs.StringArrayVar(&f.Releases, "releases", f.Releases, "openshift releases to collect data from")
	fs.StringArrayVar(&f.Architectures, "architectures", f.Architectures, "architectures to collect data from")
}

func NewBigQueryReleaseUploadFlagsCommand() *cobra.Command {
	f := NewBigQueryReleaseUploadFlags()

	cmd := &cobra.Command{
		Use:          "upload-releases",
		Long:         `Upload release data to bigquery`,
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
func (f *BigQueryReleaseUploadFlags) Validate() error {
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
func (f *BigQueryReleaseUploadFlags) ToOptions(ctx context.Context) (*allReleaseUploaderOptions, error) {
	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)
	httpClient := &http.Client{Timeout: 60 * time.Second}
	ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)

	return &allReleaseUploaderOptions{
		ciDataClient:  ciDataClient,
		ciDataSet:     ciDataSet,
		httpClient:    httpClient,
		releases:      f.Releases,
		architectures: f.Architectures,
	}, nil
}

type BigQueryReleaseTableCreateFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
}

func NewBigQueryReleaseTableCreateFlags() *BigQueryReleaseTableCreateFlags {
	return &BigQueryReleaseTableCreateFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryReleaseTableCreateFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
}

func NewBigQueryReleaseTableCreateFlagsCommand() *cobra.Command {
	f := NewBigQueryReleaseTableCreateFlags()

	cmd := &cobra.Command{
		Use:          "create-releases",
		Long:         `Create release tables in bigquery`,
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
func (f *BigQueryReleaseTableCreateFlags) Validate() error {
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
func (f *BigQueryReleaseTableCreateFlags) ToOptions(ctx context.Context) (*allReleaseTableCreatorOptions, error) {
	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)
	ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)

	return &allReleaseTableCreatorOptions{
		ciDataClient: ciDataClient,
		ciDataSet:    ciDataSet,
	}, nil
}
