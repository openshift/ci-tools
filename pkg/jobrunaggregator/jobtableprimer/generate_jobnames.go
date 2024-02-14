package jobtableprimer

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type generateJobNamesFlags struct {
	nameGenerator *jobNameGenerator
}

func newGenerateJobNamesFlags() *generateJobNamesFlags {
	return &generateJobNamesFlags{
		nameGenerator: newJobNameGenerator(),
	}
}

func (f *generateJobNamesFlags) BindFlags(fs *pflag.FlagSet) {
}

func NewGenerateJobNamesCommand() *cobra.Command {
	f := newGenerateJobNamesFlags()

	cmd := &cobra.Command{
		Use:          "generate-job-names",
		Long:         `generate the list of jobnames and output them to stdout`,
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
func (f *generateJobNamesFlags) Validate() error {
	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *generateJobNamesFlags) ToOptions(ctx context.Context) (*GenerateJobNamesOptions, error) {
	ret := &GenerateJobNamesOptions{
		nameGenerator: f.nameGenerator,
	}
	return ret, nil
}

type GenerateJobNamesOptions struct {
	nameGenerator *jobNameGenerator
}

type FakeReleaseConfig struct {
	Verify map[string]FakeReleaseConfigVerify
}
type FakeReleaseConfigVerify struct {
	ProwJob FakeProwJob
}
type FakeProwJob struct {
	Name string
}

type FakePeriodicConfig struct {
	Periodics []FakePeriodic `yaml:"periodics"`
}
type FakePeriodic struct {
	Name string `yaml:"name"`
}

func (o *GenerateJobNamesOptions) Run(ctx context.Context) error {
	lines := []string{}
	jobNames, err := o.nameGenerator.GenerateJobNames()
	if err != nil {
		return err
	}
	lines = append(lines, "// generated using `./job-run-aggregator generate-job-names`")
	lines = append(lines, "")
	lines = append(lines, jobNames...)

	fmt.Println(strings.Join(lines, "\n"))

	return nil
}
