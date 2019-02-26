package main

import (
	"flag"
	"os"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
	"github.com/openshift/ci-operator/pkg/api"
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

	var toCommit []config.Info
	if err := config.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.FilePathElements) error {
		if (o.Org != "" && o.Org != repoInfo.Org) || (o.Repo != "" && o.Repo != repoInfo.Repo) {
			return nil
		}
		for _, output := range generateBranchedConfigs(o.CurrentRelease, o.FutureRelease, config.Info{Configuration: *configuration, RepoInfo: *repoInfo}) {
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

	for _, output := range toCommit {
		output.CommitTo(o.ConfigDir)
	}
}

func generateBranchedConfigs(currentRelease, futureRelease string, input config.Info) []config.Info {
	if !(promotion.PromotesOfficialImages(&input.Configuration) && input.Configuration.PromotionConfiguration.Name == currentRelease) {
		return nil
	}
	input.Logger().Info("Branching configuration.")
	// we need a deep copy and this is a simple albeit expensive hack to get there
	raw, err := yaml.Marshal(input.Configuration)
	if err != nil {
		input.Logger().WithError(err).Error("failed to marshal input CI Operator configuration")
		return nil
	}
	var futureConfig api.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(raw, &futureConfig); err != nil {
		input.Logger().WithError(err).Error("failed to unmarshal input CI Operator configuration")
		return nil
	}

	// in order to branch this, we need to update where we're promoting
	// to and from where we're building a release payload
	futureConfig.PromotionConfiguration.Name = futureRelease
	futureConfig.ReleaseTagConfiguration.Name = futureRelease

	futureBranchForCurrentPromotion, futureBranchForFuturePromotion, err := promotion.DetermineReleaseBranches(currentRelease, futureRelease, input.RepoInfo.Branch)
	if err != nil {
		input.Logger().WithError(err).Error("could not determine future branch that would promote to current imagestream")
		return nil
	}

	return []config.Info{
		// this config keeps the current promotion but runs on a new branch
		{Configuration: input.Configuration, RepoInfo: copyInfoSwappingBranches(input.RepoInfo, futureBranchForCurrentPromotion)},
		// this config is the future promotion on the future branch
		{Configuration: futureConfig, RepoInfo: copyInfoSwappingBranches(input.RepoInfo, futureBranchForFuturePromotion)},
	}
}

func copyInfoSwappingBranches(input config.FilePathElements, newBranch string) config.FilePathElements {
	intermediate := &input
	output := *intermediate
	output.Branch = newBranch
	output.Filename = strings.Replace(output.Filename, input.Branch, newBranch, -1)
	return output
}
