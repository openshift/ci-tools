package main

import (
	"flag"
	"os"

	"github.com/getlantern/deepcopy"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/migrate"
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

// This tool is intended to make the process of branching and duplicating configuration for
// the CI Operator easy across many repositories.
//
// We select a set of repositories to operate on by looking at which are actively promoting
// images into a specific OpenShift release, provided by `--current-release`. Branches of
// repos that actively promote to this release are considered to be our dev branches.
//
// Once we've chosen a set of configurations to operate on, we can do one of two actions:
//  - mirror configuration out, copying the development branch config to all branches for
//    the provided `--future-release` values, not changing the configuration for the dev
//    branch and making sure that the release branch for the version that matches that in
//    the dev branch has a disabled promotion stanza to ensure only one branch feeds a
//    release ImageStream
//  - bump configuration files, moving the development branch to promote to the version in
//    the `--bump` flag, enabling the promotion in the release branch that used to match
//    the dev branch version and disabling promotion in the release branch that now matches
//    the dev branch version.
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
		for _, output := range generateMigratedConfigs(config.DataWithInfo{Configuration: *configuration, Info: *info}) {
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

func generateMigratedConfigs(input config.DataWithInfo) []config.DataWithInfo {

	if !migrate.Migrated(input.Info.Org, input.Info.Repo, input.Info.Branch) {
		logrus.Debugf("%s/%s is not migrated", input.Info.Org, input.Info.Repo)
		return nil
	}
	logrus.Infof("%s/%s is migrated", input.Info.Org, input.Info.Repo)

	var output []config.DataWithInfo
	input.Logger().Info("Migrating configuration.")
	currentConfig := input.Configuration

	var futureConfig api.ReleaseBuildConfiguration
	if err := deepcopy.Copy(&futureConfig, &currentConfig); err != nil {
		input.Logger().WithError(err).Error("failed to copy input CI Operator configuration")
		return nil
	}

	var newBaseImages map[string]api.ImageStreamTagReference
	for k, baseImage := range futureConfig.BaseImages {
		if newBaseImages == nil {
			newBaseImages = map[string]api.ImageStreamTagReference{}
		}
		if baseImage.Cluster == "" {
			baseImage.Cluster = migrate.ProwClusterURL
		}
		newBaseImages[k] = baseImage
	}
	futureConfig.BaseImages = newBaseImages

	if futureConfig.ReleaseTagConfiguration != nil && futureConfig.ReleaseTagConfiguration.Cluster == "" {
		futureConfig.ReleaseTagConfiguration.Cluster = migrate.ProwClusterURL
	}

	if futureConfig.BuildRootImage != nil && futureConfig.BuildRootImage.ImageStreamTagReference != nil && futureConfig.BuildRootImage.ImageStreamTagReference.Cluster == "" {
		futureConfig.BuildRootImage.ImageStreamTagReference.Cluster = migrate.ProwClusterURL
	}

	// this config will promote to the new location on the release branch
	output = append(output, config.DataWithInfo{Configuration: futureConfig, Info: input.Info})
	return output
}
