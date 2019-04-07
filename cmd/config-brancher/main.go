package main

import (
	"flag"
	"os"

	"github.com/getlantern/deepcopy"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-operator/pkg/api"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
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
		var outputs []config.DataWithInfo
		if o.Unmirror {
			outputs = generateUnmirroredConfigs(o.CurrentRelease, o.FutureRelease, config.DataWithInfo{Configuration: *configuration, Info: *info})
		} else {
			outputs = generateBranchedConfigs(o.CurrentRelease, o.FutureRelease, config.DataWithInfo{Configuration: *configuration, Info: *info}, o.Mirror)
		}
		for _, output := range outputs {
			if !o.Confirm {
				output.Logger().Info("Would commit new file.")
				continue
			}

			// we are walking the config so we need to commit once we're done
			toCommit = append(toCommit, output)
		}

		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

	var failed bool
	for _, output := range toCommit {
		if err := output.CommitTo(o.ConfigDir); err != nil {
			failed = true
		}
	}
	if failed {
		logrus.Fatal("Failed to commit configuration to disk.")
	}
}

func generateBranchedConfigs(currentRelease, futureRelease string, input config.DataWithInfo, mirror bool) []config.DataWithInfo {
	if !(promotion.PromotesOfficialImages(&input.Configuration) && input.Configuration.PromotionConfiguration.Name == currentRelease) {
		return nil
	}
	input.Logger().Info("Branching configuration.")
	var currentConfig, futureConfig api.ReleaseBuildConfiguration
	currentConfig = input.Configuration
	if err := deepcopy.Copy(&futureConfig, &currentConfig); err != nil {
		input.Logger().WithError(err).Error("failed to copy input CI Operator configuration")
		return nil
	}

	if mirror {
		// in order to mirror this, we need to keep the promotion the same
		// but disable it on the current config
		currentConfig.PromotionConfiguration.Disabled = true
	} else {
		// in order to branch this, we need to update where we're promoting
		// to and from where we're building a release payload
		futureConfig.PromotionConfiguration.Name = futureRelease
		futureConfig.ReleaseTagConfiguration.Name = futureRelease
	}

	futureBranchForCurrentPromotion, futureBranchForFuturePromotion, err := promotion.DetermineReleaseBranches(currentRelease, futureRelease, input.Info.Branch)
	if err != nil {
		input.Logger().WithError(err).Error("could not determine future branch that would promote to current imagestream")
		return nil
	}

	return []config.DataWithInfo{
		// this config keeps the current promotion but runs on a new branch
		{Configuration: input.Configuration, Info: copyInfoSwappingBranches(input.Info, futureBranchForCurrentPromotion)},
		// this config is the future promotion on the future branch
		{Configuration: futureConfig, Info: copyInfoSwappingBranches(input.Info, futureBranchForFuturePromotion)},
	}
}

func copyInfoSwappingBranches(input config.Info, newBranch string) config.Info {
	intermediate := &input
	output := *intermediate
	output.Branch = newBranch
	return output
}

func generateUnmirroredConfigs(currentRelease, futureRelease string, input config.DataWithInfo) []config.DataWithInfo {
	if !(promotion.BuildsOfficialImages(&input.Configuration) && input.Configuration.PromotionConfiguration.Name == currentRelease) {
		return nil
	}
	input.Logger().Info("Unmirroring configuration.")
	if input.Configuration.PromotionConfiguration.Disabled {
		// this will become the official branch to promote, so we just
		// need to enable promotion
		input.Configuration.PromotionConfiguration.Disabled = false
	} else {
		// this is the current promotion/dev branch, so it needs to be
		// bumped
		input.Configuration.PromotionConfiguration.Name = futureRelease
		input.Configuration.ReleaseTagConfiguration.Name = futureRelease
	}
	return []config.DataWithInfo{input}
}
