package jobtableprimer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type generateJobNamesFlags struct {
	periodicURLs      []string
	releaseConfigURLs []string
}

func newGenerateJobNamesFlags() *generateJobNamesFlags {
	return &generateJobNamesFlags{
		periodicURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-master-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/multiarch/openshift-multiarch-master-periodics.yaml",

			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.10-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.11-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.12-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.13-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.14-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.15-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.16-periodics.yaml",

			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.13-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.14-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.15-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.16-periodics.yaml",
		},
		releaseConfigURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16.json",
		},
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
		periodicURLs:      f.periodicURLs,
		releaseConfigURLs: f.releaseConfigURLs,
	}

	sort.Strings(ret.periodicURLs)
	sort.Strings(ret.releaseConfigURLs)

	return ret, nil
}

type GenerateJobNamesOptions struct {
	periodicURLs      []string
	releaseConfigURLs []string
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
	lines = append(lines, "// generated using `./job-run-aggregator generate-job-names`")
	lines = append(lines, "")

	for _, url := range o.releaseConfigURLs {
		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("error reading %v: %v", url, resp.StatusCode)
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}
		resp.Body.Close()

		releaseConfig := &FakeReleaseConfig{}
		if err := json.Unmarshal(content, releaseConfig); err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}

		lines = append(lines, fmt.Sprintf("// begin %v", url))
		localLines := []string{}
		for _, curr := range releaseConfig.Verify {
			localLines = append(localLines, curr.ProwJob.Name)
		}
		sort.Strings(localLines)
		lines = append(lines, localLines...)
		lines = append(lines, fmt.Sprintf("// end %v", url))
		lines = append(lines, "")
	}

	for _, url := range o.periodicURLs {
		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("error reading %v: %v", url, resp.StatusCode)
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}
		resp.Body.Close()

		periodicConfig := &FakePeriodicConfig{}
		if err := yaml.Unmarshal(content, periodicConfig); err != nil {
			return fmt.Errorf("error reading %v: %w", url, err)
		}

		lines = append(lines, fmt.Sprintf("// begin %v", url))
		localLines := []string{}
		for _, curr := range periodicConfig.Periodics {
			// TODO: the single file for say "master" actually contains every release, but we only want jobs 4.10+
			// where we started disruption monitoring. Adding a bunch of future rows to buy us time but this could
			// stand some logic.
			if !strings.Contains(curr.Name, "-4.10") &&
				!strings.Contains(curr.Name, "-4.11") &&
				!strings.Contains(curr.Name, "-4.12") &&
				!strings.Contains(curr.Name, "-4.13") &&
				!strings.Contains(curr.Name, "-4.14") &&
				!strings.Contains(curr.Name, "-4.15") &&
				!strings.Contains(curr.Name, "-4.16") &&
				!strings.Contains(curr.Name, "-4.17") &&
				!strings.Contains(curr.Name, "-4.18") &&
				!strings.Contains(curr.Name, "-4.19") &&
				!strings.Contains(curr.Name, "-4.20") {
				continue
			}

			// Disruptive jobs can dramatically alter our data for certain NURP combos:
			if strings.Contains(curr.Name, "-disruptive") {
				continue
			}

			// Microshift is not yet stable, jobs are not clearly named, and we're unsure what platform/topology
			// they should be lumped in with.
			// Today they run using a single UPI GCP vm, HA may be coming later.
			if strings.Contains(curr.Name, "microshift") {
				continue
			}

			// OKD jobs are not something we monitor and keep slipping into our disruption data skewing results quite badly.
			if strings.Contains(curr.Name, "-okd") {
				continue
			}

			localLines = append(localLines, curr.Name)
		}
		sort.Strings(localLines)
		lines = append(lines, localLines...)
		lines = append(lines, fmt.Sprintf("// end %v", url))
		lines = append(lines, "")
	}

	fmt.Println(strings.Join(lines, "\n"))

	return nil
}
