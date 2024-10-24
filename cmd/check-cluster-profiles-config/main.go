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
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	configResolverAddress = api.URLForService(api.ServiceConfig)
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

	list, err := loadConfig(o.configPath)
	if err != nil {
		logger.WithError(err).Fatal("failed to load cluster profiles from config file")
	}

	if err := validator.Validate(list); err != nil {
		logger.WithError(err).Fatal("failed to validate cluster profiles")
	}

	if err := validator.checkCiSecrets(); err != nil {
		logger.WithError(err).Fatal("failed to validate secrets for cluster profiles")
	}

	logger.Info("Cluster profiles successfully checked.")
}

func loadConfig(configPath string) (api.ClusterProfilesList, error) {
	configContents, err := os.ReadFile(configPath)
	if err != nil {
		return api.ClusterProfilesList{}, fmt.Errorf("failed to read cluster profiles config: %w", err)
	}

	var profilesList api.ClusterProfilesList
	if err = yaml.Unmarshal(configContents, &profilesList); err != nil {
		return api.ClusterProfilesList{}, fmt.Errorf("failed to unmarshall file %s: %w", configPath, err)
	}

	return profilesList, nil
}

func (validator *profileValidator) Validate(profiles api.ClusterProfilesList) error {
	for _, p := range profiles {
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
		profileDetails, err := server.NewResolverClient(configResolverAddress).ClusterProfile(p.Name())
		if err != nil {
			return fmt.Errorf("failed to retrieve details from config resolver for '%s' cluster profile", p.Name())
		}
		ciSecret := &coreapi.Secret{}
		err = validator.kubeClient.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: profileDetails.Secret}, ciSecret)
		if err != nil {
			return fmt.Errorf("failed to get secret '%s' for cluster profile '%s': %w", profileDetails.Secret, p.Name(), err)
		}
	}
	return nil
}
