package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const (
	prowgenConfigFile = ".config.prowgen"
)

type options struct {
	dryRun         bool
	configDir      string
	destinationDir string
	fromOrg        string
	toOrg          string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.configDir, "config-path", "", "Path to directory containing ci-operator configurations")
	fs.StringVar(&o.destinationDir, "destination-path", "", "Path of the destination of the generated ci-operator configurations")

	fs.StringVar(&o.fromOrg, "from-org", "", "Mirror the ci-operator configuration files from a single organization, if specified")
	fs.StringVar(&o.toOrg, "to-org", "", "Name of the organization which the ci-operator configuration files will be mirrored")

	fs.BoolVar(&o.dryRun, "dry-run", false, "Whether to actually generate the new ci-operator configuration files")

	fs.Parse(os.Args[1:])
	return o
}

func (o *options) validate() error {
	if len(o.configDir) == 0 {
		return errors.New("--config-path is not defined")
	}
	if len(o.destinationDir) == 0 && !o.dryRun {
		return errors.New("--destination-path is not defined")
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

	prowgenConfig := config.Prowgen{Private: true}

	dryBuff := &bytes.Buffer{}
	callback := func(rbc *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := logrus.WithFields(logrus.Fields{"org": repoInfo.Org, "repo": repoInfo.Repo})

		if len(o.fromOrg) > 0 && repoInfo.Org != o.fromOrg {
			logger.Warning("Skipping...")
			return nil
		}

		logger.Info("Processing...")
		data, err := yaml.Marshal(rbc)
		if err != nil {
			logger.WithError(err).Fatal("couldn't marshal ci-operator's configuration")
		}

		filename := fmt.Sprintf("%s-%s-%s.yaml", o.toOrg, repoInfo.Repo, repoInfo.Branch)
		orgRepoDestinationDir := filepath.Join(o.destinationDir, o.toOrg, repoInfo.Repo)
		fileFullPath := filepath.Join(orgRepoDestinationDir, filename)

		if o.dryRun {
			dryBuff.WriteString(fmt.Sprintf("%s:\n%v\n", fileFullPath, string(data)))
			return nil
		}

		if err := os.MkdirAll(orgRepoDestinationDir, 0755); err != nil {
			logger.WithError(err).Fatal("couldn't create destination's directory")
		}

		if err := ioutil.WriteFile(fileFullPath, data, 0755); err != nil {
			logger.WithError(err).Fatal("error while writing to file")
		}

		return nil
	}

	if err := config.OperateOnCIOperatorConfigDir(o.configDir, callback); err != nil {
		if err != nil {
			logrus.WithError(err).Fatal("error while generating the ci-operator configuration files")
		}
	}

	data, err := yaml.Marshal(prowgenConfig)
	if err != nil {
		logrus.WithError(err).Fatal("couldn't marshal prowgen configuration")
	}

	prowgenConfigFullPath := filepath.Join(o.destinationDir, o.toOrg, prowgenConfigFile)
	if o.dryRun {
		dryBuff.WriteString(fmt.Sprintf("%s:\n%v", prowgenConfigFullPath, string(data)))
		fmt.Printf("%s", dryBuff)
	} else {
		if err := ioutil.WriteFile(prowgenConfigFullPath, data, 0755); err != nil {
			logrus.WithError(err).Fatal("error while writing to file")
		}
	}
}
