package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/sanitizer"
)

type options struct {
	prowJobConfigDir string
	configPath       string

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flag.StringVar(&opt.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	return opt
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("Failed to parse flags")
	}

	if opt.help {
		flagSet.Usage()
		os.Exit(0)
	}

	if len(opt.prowJobConfigDir) == 0 {
		logrus.Fatal("mandatory argument --prow-jobs-dir wasn't set")
	}
	if len(opt.configPath) == 0 {
		logrus.Fatal("mandatory argument --config-path wasn't set")
	}

	config, err := dispatcher.LoadConfig(opt.configPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load config from %q", opt.configPath)
	}
	if err := config.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate the config")
	}
	args := flagSet.Args()
	if len(args) == 0 {
		args = append(args, "")
	}
	for _, subDir := range args {
		subDir = filepath.Join(opt.prowJobConfigDir, subDir)
		if err := sanitizer.DeterminizeJobs(subDir, config, nil); err != nil {
			logrus.WithError(err).Fatal("Failed to determinize")
		}
	}
}
