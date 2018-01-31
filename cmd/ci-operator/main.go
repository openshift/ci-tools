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

var (
	rawBuildConfig = flag.String("build-config", "", "Configuration for the build to run, as JSON.")
)

func main() {
	jobSpec, err := steps.ResolveSpecFromEnv()
	if err != nil {
		fmt.Printf("Failed to resolve job spec: %v\n", err)
		os.Exit(1)
	}

	if *rawBuildConfig == "" {
		fmt.Println("Job configuration must be provided with `--buildConfig`")
		os.Exit(1)
	}

	buildConfig := &api.ReleaseBuildConfiguration{}
	if err := json.Unmarshal([]byte(*rawBuildConfig), buildConfig); err != nil {
		fmt.Printf("Malformed build configuration: %v\n", err)
		os.Exit(1)
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Failed to load in-cluster config: %v\n", err)
		os.Exit(1)
	}

	buildSteps, err := steps.FromConfig(buildConfig, jobSpec, clusterConfig)
	if err != nil {
		fmt.Printf("Failed to generate steps from config: %v\n", err)
		os.Exit(1)
	}

	graph := api.BuildGraph(buildSteps)

	if err := steps.Run(graph); err != nil {
		fmt.Printf("Failed to run steps: %v\n", err)
		os.Exit(1)
	}
}
