package jobtableprimer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	prowConfig "k8s.io/test-infra/prow/config"

	jobConfig "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	// When running in CI, we clone both openshift/ci-tools and openshift/release with the working directory being
	// openshift/ci-tools. In that context, the relative path for the release repo would be the below paths.
	defaultProwJobsDir      = "../../openshift/release/ci-operator/jobs"
	defaultReleaseConfigDir = "../../openshift/release/core-services/release-controller/_releases"

	// Any filename with a minor version lower than this will be ignored, for upgrade jobs this will refer to the desired upgrade.
	minMinor4Version = 10
)

var (
	// Regex to fetch ocp version from file names, this should cover upgrades and hit only on the first version.
	// Meaning 4.10-upgrade-from-4.9 will hit on 4.10, so that if we wish to only use data for 4.10 we still capture
	// 4.10 upgrades from 4.9
	ocpVersionRegex = regexp.MustCompile(`.*?(?P<version>4\.[0-9]+).*?`)

	ignoreSubStringJobNames = []string{
		// Disruptive jobs can dramatically alter our data for certain NURP combos:
		"-disruptive",
		// Microshift is not yet stable, jobs are not clearly named, and we're unsure what platform/topology
		// they should be lumped in with.
		// Today they run using a single UPI GCP vm, HA may be coming later.
		"microshift",
		// OKD jobs are not something we monitor and keep slipping into our disruption data skewing results quite badly.
		"-okd",
		// We ignore files that are private locations
		"-priv",
	}
)

type generateJobNamesFlags struct {
	prowJobsDir      string
	releaseConfigDir string
}

func newGenerateJobNamesFlags() *generateJobNamesFlags {
	return &generateJobNamesFlags{}
}

func (f *generateJobNamesFlags) BindFlags(fs *pflag.FlagSet) {
	fs.StringVarP(&f.prowJobsDir, "prow-jobs-dir", "", defaultProwJobsDir, "prow jobs dir to traverse")
	fs.StringVarP(&f.releaseConfigDir, "release-config-dir", "", defaultReleaseConfigDir, "release dir to traverse")
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
		prowJobsDir:      f.prowJobsDir,
		releaseConfigDir: f.releaseConfigDir,
	}

	return ret, nil
}

type GenerateJobNamesOptions struct {
	prowJobsDir      string
	releaseConfigDir string
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

func (o *GenerateJobNamesOptions) Run(ctx context.Context) error {
	lines := []string{}
	lines = append(lines, "// generated using `./job-run-aggregator generate-job-names`")
	lines = append(lines, "")

	releaseConfigLines, err := processesReleaseConfigDir(o.releaseConfigDir)
	if err != nil {
		return err
	}

	lines = append(lines, releaseConfigLines...)

	prowJobLines, err := processProwJobsDir(o.prowJobsDir)
	if err != nil {
		return err
	}

	lines = append(lines, prowJobLines...)

	fmt.Println(strings.Join(lines, "\n"))

	return nil
}

func shouldIgnoreFile(fileName string) bool {
	match := ocpVersionRegex.FindStringSubmatch(fileName)
	if len(match) == 0 {
		// We only parse files that contain 4.x versions
		return true
	}

	// If file contains any of the ignore substrings we ignore
	for _, subString := range ignoreSubStringJobNames {
		if strings.Contains(fileName, subString) {
			return true
		}
	}

	version := match[ocpVersionRegex.SubexpIndex("version")]
	splitVersion := strings.Split(version, ".")

	// If for some reason the split reveals more than x.x values, we ignore.
	// Files must have major.minor in names.
	if len(splitVersion) != 2 {
		return true
	}

	// Convert minor version to int and match against minimum minor version we care about.
	// This should never error if we got to this point since the regex would not hit on the 4.[0-9]+ name.
	minorVersion, err := strconv.Atoi(splitVersion[1])
	if err != nil {
		return true
	}
	if minorVersion < minMinor4Version {
		return true
	}

	return false
}

func processProwJobsDir(prowJobsDir string) ([]string, error) {
	lines := []string{}
	// We traverse the files with this to be able to also get filename information for recording where
	// the job names came from.
	if err := jobConfig.OperateOnJobConfigDir(prowJobsDir, func(config *prowConfig.JobConfig, elements *jobConfig.Info) error {
		allPeriodics := config.AllPeriodics()
		if len(allPeriodics) == 0 {
			return nil
		}
		localLines := []string{}
		for _, periodic := range allPeriodics {
			if shouldIgnoreFile(periodic.Name) {
				continue
			}

			localLines = append(localLines, periodic.Name)
		}

		// If all the lines were skipped or no periodics were found, we don't need to continue
		if len(localLines) == 0 {
			return nil
		}

		// Sort for consistency
		sort.Strings(localLines)

		// Wrap with `// begin <file_name>` and `// end <file_name>`
		// base dir and filename should be sufficient information for origin of data.
		// full paths can be noisy
		localLines = wrapWithLocationBlock(localLines, elements.Filename)

		lines = append(lines, localLines...)
		lines = append(lines, "")
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to load all Prow jobs: %w", err)
	}
	return lines, nil
}

func processesReleaseConfigDir(configDir string) ([]string, error) {
	lines := []string{}
	if err := filepath.WalkDir(configDir, func(fullPath string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(fullPath) != ".json" {
			return nil
		}

		content, err := gzip.ReadFileMaybeGZIP(fullPath)
		if err != nil {
			return fmt.Errorf("could not read release controller config at %s: %w", fullPath, err)
		}

		releaseConfig := &FakeReleaseConfig{}
		if err := json.Unmarshal(content, releaseConfig); err != nil {
			return fmt.Errorf("error reading %v: %w", fullPath, err)
		}

		localLines := []string{}
		for _, curr := range releaseConfig.Verify {
			if shouldIgnoreFile(curr.ProwJob.Name) {
				continue
			}
			localLines = append(localLines, curr.ProwJob.Name)
		}

		// If all the lines were skipped or not there, we don't need to continue
		if len(localLines) == 0 {
			return nil
		}

		// Sort for consistency
		sort.Strings(localLines)

		// Wrap with `// begin <file_name>` and `// end <file_name>`
		localLines = wrapWithLocationBlock(localLines, fullPath)

		lines = append(lines, localLines...)
		lines = append(lines, "")
		return nil
	}); err != nil {
		return nil, fmt.Errorf("could not process input configurations. %v: %w", configDir, err)
	}
	return lines, nil
}

// wrapWithLocationBlock wraps the given array with beginning and ending blocks with the file path
func wrapWithLocationBlock(lines []string, location string) []string {
	beginBlock := fmt.Sprintf("// begin %v", location)
	endBlock := fmt.Sprintf("// end %v", location)
	lines = append([]string{beginBlock}, lines...)
	lines = append(lines, endBlock)
	return lines
}
