package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
	"github.com/openshift/ci-operator/pkg/api"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
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

	var toCommit []configInfo
	if err := config.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.FilePathElements) error {
		for _, output := range generateBranchedConfigs(o.CurrentRelease, o.FutureRelease, configInfo{configuration: *configuration, repoInfo: *repoInfo}) {
			if !o.Confirm {
				output.logger().Info("Would commit new file.")
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
		output.commitTo(o.ConfigDir)
	}
}

type configInfo struct {
	configuration api.ReleaseBuildConfiguration
	repoInfo      config.FilePathElements
}

func (i *configInfo) logger() *logrus.Entry {
	return config.LoggerForInfo(i.repoInfo)
}

func (i *configInfo) commitTo(dir string) {
	raw, err := yaml.Marshal(i.configuration)
	if err != nil {
		i.logger().WithError(err).Error("failed to marshal output CI Operator configuration")
		return
	}
	outputFile := path.Join(
		dir, i.repoInfo.Org, i.repoInfo.Repo,
		fmt.Sprintf("%s-%s-%s.yaml", i.repoInfo.Org, i.repoInfo.Repo, i.repoInfo.Branch),
	)
	if err := ioutil.WriteFile(outputFile, raw, 0664); err != nil {
		i.logger().WithError(err).Error("failed to write new CI Operator configuration")
	}
}

func generateBranchedConfigs(currentRelease, futureRelease string, input configInfo) []configInfo {
	if !(promotion.PromotesOfficialImages(&input.configuration) && input.configuration.PromotionConfiguration.Name == currentRelease) {
		return nil
	}
	input.logger().Info("Branching configuration.")
	// we need a deep copy and this is a simple albeit expensive hack to get there
	raw, err := yaml.Marshal(input.configuration)
	if err != nil {
		input.logger().WithError(err).Error("failed to marshal input CI Operator configuration")
		return nil
	}
	var futureConfig api.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(raw, &futureConfig); err != nil {
		input.logger().WithError(err).Error("failed to unmarshal input CI Operator configuration")
		return nil
	}

	// in order to branch this, we need to update where we're promoting
	// to and from where we're building a release payload
	futureConfig.PromotionConfiguration.Name = futureRelease
	futureConfig.ReleaseTagConfiguration.Name = futureRelease

	futureBranchForCurrentPromotion, futureBranchForFuturePromotion, err := promotion.DetermineReleaseBranches(currentRelease, futureRelease, input.repoInfo.Branch)
	if err != nil {
		input.logger().WithError(err).Error("could not determine future branch that would promote to current imagestream")
		return nil
	}

	return []configInfo{
		// this config keeps the current promotion but runs on a new branch
		{configuration: input.configuration, repoInfo: copyInfoSwappingBranches(input.repoInfo, futureBranchForCurrentPromotion)},
		// this config is the future promotion on the future branch
		{configuration: futureConfig, repoInfo: copyInfoSwappingBranches(input.repoInfo, futureBranchForFuturePromotion)},
	}
}

func copyInfoSwappingBranches(input config.FilePathElements, newBranch string) config.FilePathElements {
	intermediate := &input
	output := *intermediate
	output.Branch = newBranch
	output.Filename = strings.Replace(output.Filename, input.Branch, newBranch, -1)
	return output
}
