package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

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
	config.WhitelistOptions
	config.Options

	toOrg   string
	onlyOrg string

	clean bool
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.toOrg, "to-org", "", "Name of the organization which the ci-operator configuration files will be mirrored")
	fs.StringVar(&o.onlyOrg, "only-org", "", "Mirror only ci-operator configuration from this organization")

	fs.BoolVar(&o.clean, "clean", true, "If the `to-org` folder already exists, then delete all subdirectories")

	o.Options.Bind(fs)
	o.WhitelistOptions.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	if err := o.Options.Validate(); err != nil {
		return fmt.Errorf("failed to validate config options: %w", err)
	}
	if err := o.Options.Complete(); err != nil {
		return fmt.Errorf("failed to complete config options: %w", err)
	}
	if len(o.toOrg) == 0 {
		return errors.New("--to-org is not defined")
	}
	return o.WhitelistOptions.Validate()
}

type configsByRepo map[string][]config.DataWithInfo

func (d configsByRepo) cleanDestinationSubdirs(destinationOrgPath string) error {
	contents, err := ioutil.ReadDir(destinationOrgPath)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", destinationOrgPath, err)
	}

	for _, info := range contents {
		if !info.IsDir() {
			continue
		}

		if err := os.RemoveAll(filepath.Join(destinationOrgPath, info.Name())); err != nil {
			return fmt.Errorf("couldn't delete dir: %w", err)
		}
	}

	return nil
}

func (d configsByRepo) generateConfigs(configDir string) error {
	for _, dataWithInfos := range d {
		for _, dataWithInfo := range dataWithInfos {
			if err := dataWithInfo.CommitTo(configDir); err != nil {
				return fmt.Errorf("couldn't create the configuration file: %w", err)
			}
		}
	}
	return nil
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid option")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	configsByRepo := make(configsByRepo)

	callback := func(rbc *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := logrus.WithFields(logrus.Fields{"org": repoInfo.Org, "repo": repoInfo.Repo, "branch": repoInfo.Branch})

		if repoInfo.Org == o.toOrg {
			return nil
		}

		if len(o.onlyOrg) > 0 && repoInfo.Org != o.onlyOrg {
			logger.Warnf("Skipping... This repo doesn't belong in %s organization", o.onlyOrg)
			return nil
		}

		if !promotion.BuildsOfficialImages(rbc, promotion.WithoutOKD) && !o.WhitelistConfig.IsWhitelisted(repoInfo) {
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

		for name, release := range rbc.Releases {
			if release.Integration != nil {
				updated := *release.Integration
				privateIntegrationRelease(&updated)
				rbc.Releases[name] = api.UnresolvedRelease{Integration: &updated}
			}
		}

		if rbc.BuildRootImage != nil && rbc.BuildRootImage.ImageStreamTagReference != nil {
			privateBuildRoot(rbc.BuildRootImage)
		}

		if len(rbc.BaseImages) > 0 {
			privateBaseImages(rbc.BaseImages)
		}

		if len(rbc.BaseRPMImages) > 0 {
			privateBaseImages(rbc.BaseRPMImages)
		}

		if rbc.PromotionConfiguration != nil {
			if !promotion.BuildsOfficialImages(rbc, promotion.WithoutOKD) && o.WhitelistConfig.IsWhitelisted(repoInfo) {
				logger.Warn("Repo is whitelisted. Disable promotion...")
				rbc.PromotionConfiguration.Disabled = true
			}
			privatePromotionConfiguration(rbc.PromotionConfiguration)
		}
		// don't copy periodics and postsubmits
		var tests []api.TestStepConfiguration
		for _, test := range rbc.Tests {
			if test.Cron == nil && test.Interval == nil && !test.Postsubmit {
				tests = append(tests, test)
			}
		}
		rbc.Tests = tests

		repoInfo.Org = o.toOrg
		rbc.Metadata.Org = o.toOrg
		configsByRepo[repoInfo.Repo] = append(configsByRepo[repoInfo.Repo], config.DataWithInfo{
			Configuration: *rbc,
			Info:          *repoInfo,
		})

		return nil
	}

	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, callback); err != nil {
		logrus.WithError(err).Fatal("error while operating in the ci-operator configuration files")
	}

	dest := filepath.Join(o.ConfigDir, o.toOrg)
	logger := logrus.WithField("destination", dest)
	if o.clean {
		logger.Info("Cleaning destination's sub directories")
		if err := configsByRepo.cleanDestinationSubdirs(dest); err != nil {
			logger.WithError(err).Fatal("couldn't cleanup destination's subdirectories")
		}
	}

	logger.Info("Generating configurations...")
	if err := configsByRepo.generateConfigs(o.ConfigDir); err != nil {
		logger.WithError(err).Fatal("couldn't generate configurations")
	}
}

func privateReleaseTagConfiguration(tagSpecification *api.ReleaseTagConfiguration) {
	if tagSpecification.Namespace == ocpNamespace {
		tagSpecification.Name = fmt.Sprintf("%s-priv", tagSpecification.Name)
		tagSpecification.Namespace = privatePromotionNamespace
	}
}

func privateIntegrationRelease(release *api.Integration) {
	if release.Namespace == ocpNamespace {
		release.Name = fmt.Sprintf("%s-priv", release.Name)
		release.Namespace = privatePromotionNamespace
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
		if reference.Namespace == ocpNamespace && isIntegrationImageStream(reference.Name) {
			reference.Name = fmt.Sprintf("%s-priv", reference.Name)
			reference.Namespace = privatePromotionNamespace
			baseImages[name] = reference
		}
	}
}

func privatePromotionConfiguration(promotion *api.PromotionConfiguration) {
	if promotion.Namespace == ocpNamespace {
		promotion.Name = fmt.Sprintf("%s-priv", promotion.Name)
		promotion.Namespace = privatePromotionNamespace
	}
}

func strP(str string) *string {
	return &str
}

func isIntegrationImageStream(name string) bool {
	return strings.HasPrefix(name, "4.")
}
