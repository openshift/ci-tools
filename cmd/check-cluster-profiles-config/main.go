package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	configPath string
}

type profileValidator struct {
	profiles   api.ClusterProfilesMap
	kubeClient ctrlruntimeclient.Client
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.configPath, "config-path", "", "Path to the cluster profile config file")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse arguments")
	}

	return o
}

func newValidator(client ctrlruntimeclient.Client) *profileValidator {
	return &profileValidator{
		profiles:   make(api.ClusterProfilesMap),
		kubeClient: client,
	}
}

func main() {
	logger := logrus.WithField("component", "check-cluster-profiles-config")
	o := gatherOptions()

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}
	client, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("failed to create client")
	}
	validator := newValidator(client)

	if err := validator.loadConfig(o.configPath); err != nil {
		logger.WithError(err).Fatal("failed to load profiles from config")
	}

	if err := validator.checkCiSecrets(); err != nil {
		logger.WithError(err).Fatal("failed to check secrets for cluster profiles")
	}

	logger.Info("Cluster profiles successfully checked.")
}

func (validator *profileValidator) loadConfig(configPath string) error {
	configContents, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read cluster profiles config: %w", err)
	}

	var profilesList api.ClusterProfilesList
	if err = yaml.Unmarshal(configContents, &profilesList); err != nil {
		return fmt.Errorf("failed to unmarshall file %s: %w", configPath, err)
	}

	for _, p := range profilesList {
		// Check if a profile isn't already defined in the config
		if _, found := validator.profiles[p.Profile]; found {
			return fmt.Errorf("cluster profile '%v' already exists in the configuration file", p.Profile)
		}
		validator.profiles[p.Profile] = p
	}
	return nil
}

// checkCiSecrets verifies that the secret for each cluster profile exists in the ci namespace
func (validator *profileValidator) checkCiSecrets() error {
	for p := range validator.profiles {
		ciSecret := &coreapi.Secret{}
		err := validator.kubeClient.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: p.Secret()}, ciSecret)
		if err != nil {
			return fmt.Errorf("failed to get secret '%s' for cluster profile '%s': %w", p.Secret(), p.Name(), err)
		}
	}
	return nil
}
