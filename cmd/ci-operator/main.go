package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/openshift/ci-operator/pkg/api"
	"github.com/openshift/ci-operator/pkg/steps"
)

func bindOptions() *options {
	opt := &options{}
	flag.StringVar(&opt.rawBuildConfig, "build-config", "", "Configuration for the build to run, as JSON.")
	flag.BoolVar(&opt.dry, "dry-run", true, "Do not contact the API server.")
	return opt
}

type options struct {
	rawBuildConfig string
	dry            bool

	buildConfig   *api.ReleaseBuildConfiguration
	jobSpec       *steps.JobSpec
	clusterConfig *rest.Config
}

func (o *options) Validate() error {
	if o.rawBuildConfig == "" {
		return fmt.Errorf("job configuration must be provided with `--build-config`")
	}
	return nil
}

func (o *options) Complete() error {
	jobSpec, err := steps.ResolveSpecFromEnv()
	if err != nil {
		return fmt.Errorf("failed to resolve job spec: %v\n", err)
	}
	o.jobSpec = jobSpec

	if err := json.Unmarshal([]byte(o.rawBuildConfig), &o.buildConfig); err != nil {
		return fmt.Errorf("malformed build configuration: %v\n", err)
	}

	if o.dry {
		o.clusterConfig = nil
	} else {
		clusterConfig, err := rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("failed to load in-cluster config: %v\n", err)
		}
		o.clusterConfig = clusterConfig
	}

	return nil
}

func (o *options) Run() error {
	buildSteps, err := steps.FromConfig(o.buildConfig, o.jobSpec, o.clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to generate steps from config: %v\n", err)
	}

	graph := api.BuildGraph(buildSteps)

	if err := steps.Run(graph, o.dry); err != nil {
		return fmt.Errorf("failed to run steps: %v\n", err)
	}

	return nil
}

func main() {
	opt := bindOptions()
	flag.Parse()

	if err := opt.Validate(); err != nil {
		fmt.Printf("Invalid options: %v", err)
		os.Exit(1)
	}

	if err := opt.Complete(); err != nil {
		fmt.Printf("Invalid environment: %v", err)
		os.Exit(1)
	}

	if err := opt.Run(); err != nil {
		fmt.Printf("Failed: %v", err)
		os.Exit(1)
	}
}
