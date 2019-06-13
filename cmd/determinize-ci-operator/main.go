package main

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func gatherOptions() promotion.Options {
	o := promotion.Options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	var toCommit []config.DataWithInfo
	if err := config.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		if (o.Org != "" && o.Org != info.Org) || (o.Repo != "" && o.Repo != info.Repo) {
			return nil
		}
		if !(promotion.PromotesOfficialImages(configuration) && configuration.PromotionConfiguration.Name == o.CurrentRelease) {
			return nil
		}
		output := config.DataWithInfo{Configuration: *configuration, Info: *info}
		if !o.Confirm {
			output.Logger().Info("Would re-format file.")
			return nil
		}

		// we are walking the config so we need to commit once we're done
		toCommit = append(toCommit, output)

		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

	for _, output := range toCommit {
		output.CommitTo(o.ConfigDir)
	}
}
