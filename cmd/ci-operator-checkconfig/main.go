package main

import (
	"flag"
	"fmt"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"os"
)

func main() {
	var configDir string
	flag.StringVar(&configDir, "config-dir", "", "The directory containing configuration files.")
	flag.Parse()

	if configDir == "" {
		fmt.Println("The --config-dir flag is required but was not provided")
		os.Exit(1)
	}

	if err := config.OperateOnCIOperatorConfigDir(configDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		// validation is implicit, so we don't need to do anything
		return nil
	}); err != nil {
		fmt.Printf("error validating configuration files: %v\n", err)
		os.Exit(1)
	}
}
