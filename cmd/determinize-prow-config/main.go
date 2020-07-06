// determinize-prow-config reads and writes Prow configuration
// to enforce formatting on the files
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/sirupsen/logrus"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	prowConfigDir string
}

func (o *options) Validate() error {
	if o.prowConfigDir == "" {
		return errors.New("--prow-config-dir is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := updateProwConfig(o.prowConfigDir); err != nil {
		logrus.WithError(err).Fatal("could not update Prow configuration")
	}

	if err := updatePluginConfig(o.prowConfigDir); err != nil {
		logrus.WithError(err).Fatal("could not update Prow plugin configuration")
	}
}

func updateProwConfig(configDir string) error {
	configPath := path.Join(configDir, config.ProwConfigFile)
	agent := prowconfig.Agent{}
	if err := agent.Start(configPath, ""); err != nil {
		return fmt.Errorf("could not load Prow configuration: %w", err)
	}
	data, err := yaml.Marshal(agent.Config())
	if err != nil {
		return fmt.Errorf("could not marshal Prow configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}

func updatePluginConfig(configDir string) error {
	configPath := path.Join(configDir, config.PluginConfigFile)
	agent := plugins.ConfigAgent{}
	if err := agent.Load(configPath, false); err != nil {
		return fmt.Errorf("could not load Prow plugin configuration: %w", err)
	}
	data, err := yaml.Marshal(agent.Config())
	if err != nil {
		return fmt.Errorf("could not marshal Prow plugin configuration: %w", err)
	}

	return ioutil.WriteFile(configPath, data, 0644)
}
