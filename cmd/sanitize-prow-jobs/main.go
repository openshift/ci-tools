package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/sanitizer"
)

type options struct {
	prowJobConfigDir  string
	configPath        string
	clusterConfigPath string
	clusterOnly       bool

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flag.StringVar(&opt.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	flag.StringVar(&opt.clusterConfigPath, "cluster-config-path", "core-services/sanitize-prow-jobs/_clusters.yaml", "Path to the config file (core-services/sanitize-prow-jobs/_clusters.yaml in openshift/release)")
	flag.BoolVar(&opt.clusterOnly, "cluster-only", false, "Only apply cluster assignments; write changed files atomically.")
	flag.BoolVar(&opt.help, "h", false, "Show help for sanitize-prow-jobs")

	return opt
}

func (o options) validate() error {
	if o.prowJobConfigDir == "" {
		return fmt.Errorf("mandatory argument --prow-jobs-dir wasn't set")
	}
	if o.configPath == "" {
		return fmt.Errorf("mandatory argument --config-path wasn't set")
	}
	return nil
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

	if err := opt.validate(); err != nil {
		logrus.Fatal(err)
	}

	args := flagSet.Args()
	if len(args) == 0 {
		args = append(args, "")
	}
	config, err := dispatcher.LoadConfig(opt.configPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load config from %q", opt.configPath)
	}
	cm, blocked, err := dispatcher.LoadClusterConfig(opt.clusterConfigPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load cluster config from %q", opt.configPath)
	}
	if _, err := config.SynchronizeBuildFarm(cm); err != nil {
		logrus.WithError(err).Fatal("Failed to synchronize dispatcher config with cluster inventory")
	}
	if err := config.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate the config")
	}
	for _, subDir := range args {
		subDir = filepath.Join(opt.prowJobConfigDir, subDir)
		if opt.clusterOnly {
			if err := sanitizer.ApplyDefaultClusterAssignments(subDir, config, blocked, cm); err != nil {
				logrus.WithError(err).Fatal("Failed to apply default cluster assignments")
			}
			continue
		}
		if err := sanitizer.DeterminizeJobs(subDir, config, nil, blocked, cm); err != nil {
			logrus.WithError(err).Fatal("Failed to determinize")
		}
	}
}
