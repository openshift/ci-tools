package tablescreator

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryTablesCreateFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags
}

func NewBigQueryTablesCreateFlags() *BigQueryTablesCreateFlags {
	return &BigQueryTablesCreateFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryTablesCreateFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)
}

func NewBigQueryCreateTablesFlagsCommand() *cobra.Command {
	f := NewBigQueryTablesCreateFlags()

	cmd := &cobra.Command{
		Use:          "create-tables",
		Short:        "Create Jobs table in bigquery",
		Long:         "Create Jobs table in bigquery",
		SilenceUsage: false,

		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.Validate(); err != nil {
				logrus.WithError(err).Fatal("Flags are invalid")
			}
			logrus.Warn("create-tables command is presently a no-op until we implement a schema management solution")
			return nil
		},

		Args: jobrunaggregatorlib.NoArgs,
	}

	f.BindFlags(cmd.Flags())

	return cmd
}

// Validate checks to see if the user-input is likely to produce functional runtime options
func (f *BigQueryTablesCreateFlags) Validate() error {
	if err := f.DataCoordinates.Validate(); err != nil {
		return err
	}
	if err := f.Authentication.Validate(); err != nil {
		return err
	}

	return nil
}
