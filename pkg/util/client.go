package util

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func LoadClusterConfig() (*rest.Config, error) {
	if env := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); env != "" {
		credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			return nil, fmt.Errorf("could not load credentials from config: %w", err)
		}

		clusterConfig, err := clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("could not load client configuration: %w", err)
		}
		return clusterConfig, nil
	}

	// otherwise, prefer in-cluster config
	return rest.InClusterConfig()
}

func LoadKubeConfigs(kubeconfig string) (map[string]*rest.Config, string, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	loader.ExplicitPath = kubeconfig
	cfg, err := loader.Load()
	if err != nil {
		return nil, "", err
	}
	configs := map[string]*rest.Config{}
	var errs []error
	for context := range cfg.Contexts {
		contextCfg, err := clientcmd.NewNonInteractiveClientConfig(*cfg, context, &clientcmd.ConfigOverrides{}, loader).ClientConfig()
		if err != nil {
			// Let the caller decide if they want to handle errors
			errs = append(errs, fmt.Errorf("create %s client: %w", context, err))
			continue
		}
		configs[context] = contextCfg
		logrus.Infof("Parsed kubeconfig context: %s", context)
	}
	if kubeconfigsFromEnv := strings.Split(os.Getenv("KUBECONFIG"), ":"); len(kubeconfigsFromEnv) > 0 && len(kubeconfigsFromEnv) > len(configs) {
		errs = append(errs, fmt.Errorf("KUBECONFIG env var with value %s had %d elements but only got %d kubeconfigs", os.Getenv("KUBECONFIG"), len(kubeconfigsFromEnv), len(configs)))
	}
	return configs, cfg.CurrentContext, utilerrors.NewAggregate(errs)
}
