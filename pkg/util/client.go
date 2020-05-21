package util

import (
	"fmt"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func LoadClusterConfig() (*rest.Config, error) {
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
			errs = append(errs, fmt.Errorf("create %s client: %v", context, err))
			continue
		}
		configs[context] = contextCfg
		logrus.Infof("Parsed kubeconfig context: %s", context)
	}
	return configs, cfg.CurrentContext, utilerrors.NewAggregate(errs)
}
