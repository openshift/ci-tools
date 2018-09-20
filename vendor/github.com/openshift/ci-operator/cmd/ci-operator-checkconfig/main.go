package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"

	"github.com/openshift/ci-operator/pkg/api"
)

func main() {
	var configDir string
	flag.StringVar(&configDir, "config-dir", "", "The directory containing configuration files.")
	flag.Parse()

	if configDir == "" {
		fmt.Println("The --config-dir flag is required but was not provided")
		os.Exit(1)
	}

	if err := filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("prevent panic by handling failure accessing a path %q: %v\n", configDir, err)
			return err
		}
		if filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".json" {
			// we assume any JSON or YAML in the config dir is a CI Operator config
			name, err := filepath.Rel(configDir, path)
			if err != nil {
				return fmt.Errorf("could not determine relative path name for %s: %v", path, err)
			}

			data, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to load config from %s: %v", name, err)
			}

			var config api.ReleaseBuildConfiguration
			if err := yaml.Unmarshal(data, &config); err != nil {
				return fmt.Errorf("invalid configuration from %s: %v\nvalue:%s", name, err, string(data))
			}

			if err := config.Validate(); err != nil {
				return fmt.Errorf("invalid configuration from %s: %v", name, err)

			}
			fmt.Printf("validated configuration at %s\n", name)
		}
		return nil
	}); err != nil {
		fmt.Printf("error loading configuration files: %v\n", err)
		os.Exit(1)
	}
}
