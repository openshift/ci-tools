package config

import (
	"errors"
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

// Options holds options to load CI Operator configuration
// and select a subset of that configuration to operate on.
// Configurations can be filtered by --org, --repo, or both.
type Options struct {
	ConfigDir string
	Org       string
	Repo      string

	LogLevel string
}

func (o *Options) Validate() error {
	if o.ConfigDir == "" {
		return errors.New("required flag --config-dir was unset")
	}

	level, err := logrus.ParseLevel(o.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *Options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.ConfigDir, "config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.LogLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.Org, "org", "", "Limit repos affected to those in this org.")
	fs.StringVar(&o.Repo, "repo", "", "Limit repos affected to this repo.")
}

func (o *Options) matches(info *Info) bool {
	switch {
	case o.Org == "" && o.Repo == "":
		return true
	case o.Org != "" && o.Repo != "":
		return o.Org == info.Org && o.Repo == info.Repo
	default:
		return (o.Org != "" && o.Org == info.Org) || (o.Repo != "" && o.Repo == info.Repo)
	}
}

// OperateOnCIOperatorConfigDir filters the full set of configurations
// down to those that were selected by the user with --{org|repo}
func (o *Options) OperateOnCIOperatorConfigDir(configDir string, callback func(*cioperatorapi.ReleaseBuildConfiguration, *Info) error) error {
	return OperateOnCIOperatorConfigDir(configDir, func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *Info) error {
		if !o.matches(info) {
			return nil
		}
		return callback(configuration, info)
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
