package config

import (
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

// Options holds options to load CI Operator configuration
// and select a subset of that configuration to operate on.
// Configurations can be filtered by --org, --repo, or both.
type Options struct {
	ConfigDir string
	Org       string
	Repo      string

	LogLevel string

	onlyProcessChanges bool
	modifiedFiles      sets.Set[string]
}

func (o *Options) Validate() error {
	if o.ConfigDir == "" {
		return errors.New("required flag --config-dir was unset")
	}

	level, err := logrus.ParseLevel(o.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *Options) Complete() error {
	if !o.onlyProcessChanges {
		return nil
	}

	_, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("couldn't find git command in the system: %w", err)
	}

	mainBranch, err := getUpstreamBranch()
	if err != nil {
		return fmt.Errorf("couldn't get upstream branch: %w", err)
	}

	cmd := exec.Command("git", "diff", "--name-status", mainBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("couldn't get the changed files comparing with %s branch: %w", mainBranch, err)
	}

	trimmedLastLine := strings.TrimSuffix(string(out), "\n")
	changedFilesWithStatus := strings.Split(trimmedLastLine, "\n")

	// Fill the modified files set based on the git output. If we detect anything else than modified files,
	// turn onlyProcessChanges to false, in order to process all the files instead of a subset. For now we
	// care of processing on modified files when there are ONLY modified files.
	// Example output of `git diff --name-status master`
	// M       path/of/modified/file.yaml
	// D       path/of/deleted/file.yaml
	// R100    path/of/renamed/old-name.yaml   path/of/renamed/new-name.yaml
	o.modifiedFiles = sets.New[string]()
	for _, f := range changedFilesWithStatus {
		statusAndFile := strings.Fields(f)
		if len(statusAndFile) > 1 && statusAndFile[0] == "M" {
			o.modifiedFiles.Insert(statusAndFile[1])
			continue
		}
		o.onlyProcessChanges = false
	}

	return nil
}

func (o *Options) ProcessAll() bool {
	return !o.onlyProcessChanges
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.ConfigDir, "config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.LogLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.Org, "org", "", "Limit repos affected to those in this org.")
	fs.StringVar(&o.Repo, "repo", "", "Limit branches affected to this repo.")

	fs.BoolVar(&o.onlyProcessChanges, "only-process-changes", false, "If true, compare changes with the main branch")
}

func (o *Options) matches(org, repo string) bool {
	switch {
	case o.Org == "" && o.Repo == "":
		return true
	case o.Org != "" && o.Repo != "":
		return o.Org == org && o.Repo == repo
	default:
		return (o.Org != "" && o.Org == org) || (o.Repo != "" && o.Repo == repo)
	}
}

// OperateOnCIOperatorConfigDir filters the full set of configurations
// down to those that were selected by the user with --{org|repo}
func (o *Options) OperateOnCIOperatorConfigDir(configDir string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *Info) error) error {
	return OperateOnCIOperatorConfigDir(configDir, func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *Info) error {
		if !o.matches(info.Metadata.Org, info.Metadata.Repo) {
			return nil
		}

		if o.onlyProcessChanges && !o.modifiedFiles.Has(info.Filename) {
			return nil
		}

		return callback(configuration, info)
	})
}

// OperateOnJobConfigSubdirPaths filters the full set of configurations
// down to those that were selected by the user with --{org|repo}
func (o *Options) OperateOnJobConfigSubdirPaths(dir, subDir string, knownInfraJobFiles sets.Set[string], callback func(info *jc.Info) error) error {
	return jc.OperateOnJobConfigSubdirPaths(dir, subDir, knownInfraJobFiles, func(info *jc.Info) error {
		if !o.matches(info.Org, info.Repo) {
			return nil
		}
		if o.onlyProcessChanges && !o.modifiedFiles.Has(info.Filename) {
			return nil
		}
		return callback(info)
	})
}

type ConfirmableOptions struct {
	Options

	Confirm bool
}

func (o *ConfirmableOptions) Validate() error {
	return o.Options.Validate()
}

func (o *ConfirmableOptions) Bind(fs *flag.FlagSet) {
	o.Options.Bind(fs)
	fs.BoolVar(&o.Confirm, "confirm", false, "Create the branched configuration files.")
}

func getUpstreamBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	out, err := cmd.CombinedOutput()

	if err != nil {
		if strings.HasPrefix(string(out), "fatal: no upstream configured for branch") {
			return "master", nil
		} else {
			return "", fmt.Errorf("%s: %w", string(out), err)
		}
	}

	if len(out) > 0 {
		return strings.TrimSuffix(string(out), "\n"), nil
	}

	return "master", nil
}
