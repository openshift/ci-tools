package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	imageapi "github.com/openshift/api/image/v1"
	projectapi "github.com/openshift/api/project/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"github.com/openshift/client-go/project/clientset/versioned"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

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

	clusterConfig, err := loadClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to load cluster config: %v\n", err)
	}
	o.clusterConfig = clusterConfig

	return nil
}

// loadClusterConfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func loadClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}

	credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("could not load credentials from config: %v", err)
	}

	clusterConfig, err = clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

func (o *options) Run() error {
	buildSteps, err := steps.FromConfig(o.buildConfig, o.jobSpec, o.clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to generate steps from config: %v\n", err)
	}

	if !o.dry {
		log.Println("Setting up namespace for testing...")
		projectGetter, err := versioned.NewForConfig(o.clusterConfig)
		if err != nil {
			return fmt.Errorf("could not get project client for cluster config: %v", err)
		}

		if _, err := projectGetter.ProjectV1().ProjectRequests().Create(&projectapi.ProjectRequest{
			ObjectMeta: meta.ObjectMeta{
				Namespace: o.jobSpec.Identifier(),
				Name:      o.jobSpec.Identifier(),
			},
		}); err != nil && ! errors.IsAlreadyExists(err) {
			return fmt.Errorf("could not set up namespace for test: %v", err)
		}

		log.Println("Setting up pipeline imagestream for testing...")
		imageGetter, err := imageclientset.NewForConfig(o.clusterConfig)
		if err != nil {
			return fmt.Errorf("could not get image client for cluster config: %v", err)
		}

		if _, err := imageGetter.ImageStreams(o.jobSpec.Identifier()).Create(&imageapi.ImageStream{
			ObjectMeta: meta.ObjectMeta{
				Namespace: o.jobSpec.Identifier(),
				Name:      steps.PipelineImageStream,
			},
		}); err != nil && ! errors.IsAlreadyExists(err) {
			return fmt.Errorf("could not set up pipeline imagestream for test: %v", err)
		}
	}

	if err := steps.Run(api.BuildGraph(buildSteps), o.dry); err != nil {
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
