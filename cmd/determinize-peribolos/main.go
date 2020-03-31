package main

import (
	"errors"
	"flag"
	"io/ioutil"
	"os"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config/org"
	"k8s.io/test-infra/prow/logrusutil"
	"sigs.k8s.io/yaml"
)

type options struct {
	config string
}

func parseOptions() options {
	var o options
	if err := o.parseArgs(flag.CommandLine, os.Args[1:]); err != nil {
		logrus.Fatalf("Invalid flags: %v", err)
	}
	return o
}

func (o *options) parseArgs(flags *flag.FlagSet, args []string) error {
	flags.StringVar(&o.config, "config-path", "", "Path to org config.yaml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if o.config == "" {
		return errors.New("--config-path required")
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()

	o := parseOptions()

	raw, err := ioutil.ReadFile(o.config)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read --config-path file")
	}

	var cfg org.FullConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		logrus.WithError(err).Fatal("Failed to load configuration")
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to marshal output.")
	}

	if err := ioutil.WriteFile(o.config, out, 0666); err != nil {
		logrus.WithError(err).Fatal("Failed to write output.")
	}

	logrus.Info("Finished formatting configuration.")
}
