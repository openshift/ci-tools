package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	privateNamespace = "ocp-private"
)

type options struct {
	configDir string
	toOrg     string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.configDir, "config-path", "", "Path to directory containing ci-operator configurations")
	fs.StringVar(&o.toOrg, "to-org", "", "Name of the organization which the ci-operator configuration files will be mirrored")

	fs.Parse(os.Args[1:])
	return o
}

func (o *options) validate() error {
	if len(o.configDir) == 0 {
		return errors.New("--config-path is not defined")
	}

	if len(o.toOrg) == 0 {
		return errors.New("--to-org is not defined")
	}
	return nil
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid option")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	callback := func(rbc *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := logrus.WithFields(logrus.Fields{"org": repoInfo.Org, "repo": repoInfo.Repo, "branch": repoInfo.Branch})

		if repoInfo.Org == o.toOrg {
			return nil
		}

		if !promotion.BuildsOfficialImages(rbc) {
			logger.Warn("Skipping...")
			return nil
		}
		logger.Info("Processing...")

		if rbc.CanonicalGoRepository == nil {
			rbc.CanonicalGoRepository = strP(fmt.Sprintf("github.com/%s/%s", repoInfo.Org, repoInfo.Repo))
		}

		repoInfo.Org = o.toOrg

		rbc.PromotionConfiguration.Disabled = true
		rbc.PromotionConfiguration.Namespace = privateNamespace
		rbc.PromotionConfiguration.Name = fmt.Sprintf("%s-priv", rbc.PromotionConfiguration.Name)

		dataWithInfo := config.DataWithInfo{
			Configuration: *rbc,
			Info:          *repoInfo,
		}

		if err := dataWithInfo.CommitTo(o.configDir); err != nil {
			logger.WithError(err).Fatal("couldn't create the configuration file")
		}

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(o.configDir, callback); err != nil {
		logrus.WithError(err).Fatal("error while generating the ci-operator configuration files")
	}
}

func strP(str string) *string {
	return &str
}
