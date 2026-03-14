package main

import (
	"flag"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	config.ConfirmableOptions
}

func (o options) validate() error {
	return o.ConfirmableOptions.Validate()
}

func gatherOptions() options {
	o := options{}
	o.Bind(flag.CommandLine)
	flag.Parse()

	return o
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	if err := o.ConfirmableOptions.Complete(); err != nil {
		logrus.Fatalf("Couldn't complete the config options: %v", err)
	}

	var toCommit []config.DataWithInfo
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		output := config.DataWithInfo{Configuration: *configuration, Info: *info}
		if !o.Confirm {
			output.Logger().Info("Would re-format file.")
			return nil
		}

		// we treat the filepath as the ultimate source of truth for this
		// data, but we record it in the configuration files to ensure that
		// it's easy to consume it for downstream tools
		output.Configuration.Metadata = info.Metadata

		// we are walking the config so we need to commit once we're done
		toCommit = append(toCommit, output)

		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

	for _, output := range toCommit {
		if err := output.CommitTo(o.ConfigDir); err != nil {
			logrus.WithError(err).Fatal("commitTo failed")
		}
	}
}
