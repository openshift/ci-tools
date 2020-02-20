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
	privatePromotionNamespace = "ocp-private"
	ocpNamespace              = "ocp"
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

		if rbc.ReleaseTagConfiguration != nil {
			privateReleaseTagConfiguration(rbc.ReleaseTagConfiguration)
		}

		if rbc.BuildRootImage != nil && rbc.BuildRootImage.ImageStreamTagReference != nil {
			privateBuildRoot(rbc.BuildRootImage)
		}

		if len(rbc.BaseImages) > 0 {
			privateBaseImages(rbc.BaseImages)
		}

		if rbc.PromotionConfiguration != nil {
			privatePromotionConfiguration(rbc.PromotionConfiguration)
		}

		repoInfo.Org = o.toOrg
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

func privateReleaseTagConfiguration(tagSpecification *api.ReleaseTagConfiguration) {
	if tagSpecification.Namespace == ocpNamespace {
		tagSpecification.Name = fmt.Sprintf("%s-priv", tagSpecification.Name)
		tagSpecification.Namespace = privatePromotionNamespace
	}
}

func privateBuildRoot(buildRoot *api.BuildRootImageConfiguration) {
	if buildRoot.ImageStreamTagReference.Namespace == ocpNamespace {
		buildRoot.ImageStreamTagReference.Name = fmt.Sprintf("%s-priv", buildRoot.ImageStreamTagReference.Name)
		buildRoot.ImageStreamTagReference.Namespace = privatePromotionNamespace
	}
}

func privateBaseImages(baseImages map[string]api.ImageStreamTagReference) {
	for name, reference := range baseImages {
		if reference.Namespace == ocpNamespace {
			reference.Name = fmt.Sprintf("%s-priv", reference.Name)
			reference.Namespace = privatePromotionNamespace
			baseImages[name] = reference
		}
	}
}

func privatePromotionConfiguration(promotion *api.PromotionConfiguration) {
	if promotion.Namespace == ocpNamespace {
		promotion.Disabled = true
		promotion.Name = fmt.Sprintf("%s-priv", promotion.Name)
		promotion.Namespace = privatePromotionNamespace
	}
}

func strP(str string) *string {
	return &str
}
