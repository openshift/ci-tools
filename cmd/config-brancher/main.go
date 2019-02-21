package main

import (
	"errors"
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

type options struct {
	ciOperatorConfigDir string
	targetImageStream   string
	futureImageStream   string
	confirm             bool
}

func (o *options) Validate() error {
	if o.ciOperatorConfigDir == "" {
		return errors.New("required flag --ci-operator-config-dir was unset")
	}

	if o.targetImageStream == "" {
		return errors.New("required flag --target-imagestream was unset")
	}

	if o.futureImageStream == "" {
		return errors.New("required flag --future-imagestream was unset")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.ciOperatorConfigDir, "ci-operator-config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.targetImageStream, "target-imagestream", "", "Configurations targeting this ImageStream will get branched.")
	fs.StringVar(&o.futureImageStream, "future-imagestream", "", "Configurations will get branched to target this ImageStream.")
	fs.BoolVar(&o.confirm, "confirm", false, "Create the branched configuration files.")
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
	if err := config.OperateOnCIOperatorConfigDir(o.ciOperatorConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.FilePathElements) error {
		for _, output := range o.generateBranchedConfigs(configInfo{configuration: *configuration, repoInfo: *repoInfo}) {
			if !o.confirm {
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
		output.commitTo(o.ciOperatorConfigDir)
	}
}

type configInfo struct {
	configuration api.ReleaseBuildConfiguration
	repoInfo      config.FilePathElements
}

func (i *configInfo) logger() *logrus.Entry {
	return logrus.WithFields(logrus.Fields{
		"org":         i.repoInfo.Org,
		"repo":        i.repoInfo.Repo,
		"branch":      i.repoInfo.Branch,
		"source-file": i.repoInfo.Filename,
	})
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

func (o *options) generateBranchedConfigs(input configInfo) []configInfo {
	if !(promotion.PromotesOfficialImages(&input.configuration) && input.configuration.PromotionConfiguration.Name == o.targetImageStream) {
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
	futureConfig.PromotionConfiguration.Name = o.futureImageStream
	futureConfig.ReleaseTagConfiguration.Name = o.futureImageStream

	// futureBranchForCurrentPromotion is the branch that will promote to the current imagestream once we branch configs
	var futureBranchForCurrentPromotion string
	// futureBranchForFuturePromotion is the branch that will promote to the future imagestream once we branch configs
	var futureBranchForFuturePromotion string
	if input.repoInfo.Branch == "master" {
		futureBranchForCurrentPromotion = fmt.Sprintf("release-%s", o.targetImageStream)
		futureBranchForFuturePromotion = input.repoInfo.Branch
	} else if input.repoInfo.Branch == fmt.Sprintf("openshift-%s", o.targetImageStream) {
		futureBranchForCurrentPromotion = input.repoInfo.Branch
		futureBranchForFuturePromotion = fmt.Sprintf("openshift-%s", o.futureImageStream)
	} else {
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
