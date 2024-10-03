package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/getlantern/deepcopy"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	promotion.FutureOptions

	BumpRelease   string
	skipPeriodics bool
}

func (o *options) Validate() error {
	futureReleases := sets.New[string](o.FutureReleases.Strings()...)
	if o.BumpRelease != "" && !futureReleases.Has(o.BumpRelease) {
		return fmt.Errorf("future releases %v do not contain bump release %v", sets.List(futureReleases), o.BumpRelease)
	}

	return o.FutureOptions.Validate()
}

func (o *options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.BumpRelease, "bump-release", "", "Bump the dev config to this release and manage mirroring.")
	o.FutureOptions.Bind(fs)
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.skipPeriodics, "skip-periodics", false, "Do not duplicate periodics configuration for the current and future releases.")
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
//   - mirror configuration out, copying the development branch config to all branches for
//     the provided `--future-release` values, not changing the configuration for the dev
//     branch and making sure that the release branch for the version that matches that in
//     the dev branch has a disabled promotion stanza to ensure only one branch feeds a
//     release ImageStream
//   - bump configuration files, moving the development branch to promote to the version in
//     the `--bump` flag, enabling the promotion in the release branch that used to match
//     the dev branch version and disabling promotion in the release branch that now matches
//     the dev branch version.
func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	var toCommit []config.DataWithInfo
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, api.WithOKD, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		for _, output := range generateBranchedConfigs(o.CurrentRelease, o.BumpRelease, o.FutureReleases.Strings(), config.DataWithInfo{Configuration: *configuration, Info: *info}, o.skipPeriodics) {
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

func generateBranchedConfigs(currentRelease, bumpRelease string, futureReleases []string, input config.DataWithInfo, skipPeriodics bool) []config.DataWithInfo {
	var output []config.DataWithInfo
	input.Logger().Info("Branching configuration.")
	currentConfig := input.Configuration

	// if we are asked to bump, we need to update the config for the dev branch
	devRelease := currentRelease
	if bumpRelease != "" && promotion.IsBumpable(input.Info.Branch, currentRelease) {
		devRelease = bumpRelease
		updateRelease(&currentConfig, currentRelease, bumpRelease)
		updateImages(&currentConfig, currentRelease, bumpRelease)
		// this config will continue to run for the dev branch but will be bumped
		output = append(output, config.DataWithInfo{Configuration: currentConfig, Info: input.Info})
	}

	for _, futureRelease := range futureReleases {
		futureBranch, err := promotion.DetermineReleaseBranch(currentRelease, futureRelease, input.Info.Branch)
		if err != nil {
			input.Logger().WithError(err).Error("could not determine future branch that would promote to current imagestream")
			return nil
		}
		if futureBranch == input.Info.Branch {
			// some repos release on their dev branch, so we don't need
			// to make any changes for this one
			continue
		}

		var futureConfig api.ReleaseBuildConfiguration
		if err := deepcopy.Copy(&futureConfig, &currentConfig); err != nil {
			input.Logger().WithError(err).Error("failed to copy input CI Operator configuration")
			return nil
		}

		// the new config will point to the future release
		updateRelease(&futureConfig, devRelease, futureRelease)

		updatePromotion(&currentConfig, &futureConfig, futureRelease, devRelease)

		// users can reference the release streams via build roots or
		// input images, so we need to update those, too
		updateImages(&futureConfig, devRelease, futureRelease)
		// we need to make sure this relates to the right branch
		futureConfig.Metadata.Branch = futureBranch
		if skipPeriodics {
			removePeriodics(&futureConfig.Tests)
		}

		// this config will promote to the new location on the release branch
		output = append(output, config.DataWithInfo{Configuration: futureConfig, Info: copyInfoSwappingBranches(input.Info, futureBranch)})
	}
	return output
}

// removePeriodics removes periodic tests from the configuration
func removePeriodics(tests *[]api.TestStepConfiguration) {
	for i := len(*tests) - 1; i >= 0; i-- {
		if !(*tests)[i].Portable && (*tests)[i].IsPeriodic() {
			*tests = append((*tests)[:i], (*tests)[i+1:]...)
		}
	}
}

func updatePromotion(currentConfig, futureConfig *api.ReleaseBuildConfiguration, futureRelease, devRelease string) {
	if currentConfig.PromotionConfiguration == nil {
		return
	}

	currentPromotion := currentConfig.PromotionConfiguration
	futurePromotion := futureConfig.PromotionConfiguration

	if currentPromotion.Targets == nil {
		return
	}

	// filter and upgrade .promotion.to[] releases that promote to the current release
	newTargets := make([]api.PromotionTarget, 0, len(currentPromotion.Targets))
	for _, target := range currentPromotion.Targets {
		if target.Name == devRelease {
			target.Name = futureRelease
			target.Disabled = futureRelease == devRelease
			newTargets = append(newTargets, target)
		}
	}
	futurePromotion.Targets = newTargets
}

// updateRelease updates the release that is promoted to and that
// which is used to source the release payload for testing
func updateRelease(config *api.ReleaseBuildConfiguration, currentRelease, futureRelease string) {
	if config.PromotionConfiguration != nil {
		for i := range config.PromotionConfiguration.Targets {
			if config.PromotionConfiguration.Targets[i].Name == currentRelease {
				config.PromotionConfiguration.Targets[i].Name = futureRelease
			}
		}
	}
	if config.ReleaseTagConfiguration != nil {
		config.ReleaseTagConfiguration.Name = futureRelease
	}
	for name, release := range config.Releases {
		if release.Integration != nil {
			updated := *release.Integration
			updated.Name = futureRelease
			config.Releases[name] = api.UnresolvedRelease{Integration: &updated}
		}
		if release.Candidate != nil {
			updated := *release.Candidate
			updated.Version = futureRelease
			config.Releases[name] = api.UnresolvedRelease{Candidate: &updated}
		}
	}
}

// updateImages updates the release that is used for input images
// if it matches the release we are updating from
func updateImages(config *api.ReleaseBuildConfiguration, currentRelease, futureRelease string) {
	for name := range config.InputConfiguration.BaseImages {
		image := config.InputConfiguration.BaseImages[name]
		if api.RefersToOfficialImage(image.Namespace, api.WithOKD) && image.Name == currentRelease {
			image.Name = futureRelease
		}
		config.InputConfiguration.BaseImages[name] = image
	}

	for i := range config.InputConfiguration.BaseRPMImages {
		image := config.InputConfiguration.BaseRPMImages[i]
		if api.RefersToOfficialImage(image.Namespace, api.WithOKD) && image.Name == currentRelease {
			image.Name = futureRelease
		}
		config.InputConfiguration.BaseRPMImages[i] = image
	}

	if config.InputConfiguration.BuildRootImage != nil {
		image := config.InputConfiguration.BuildRootImage.ImageStreamTagReference
		if image != nil && api.RefersToOfficialImage(image.Namespace, api.WithOKD) && image.Name == currentRelease {
			image.Name = futureRelease
		}
		config.InputConfiguration.BuildRootImage.ImageStreamTagReference = image
	}
}

func copyInfoSwappingBranches(input config.Info, newBranch string) config.Info {
	intermediate := &input
	output := *intermediate
	output.Branch = newBranch
	return output
}
