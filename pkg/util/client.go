package util

import (
	"fmt"
	"os"

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

// LoadKubeConfig loads a kubeconfig from the file and uses the default context
func LoadKubeConfig(path string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	loader.ExplicitPath = path
	cfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("could not load kubeconfig: %w", err)
	}
	clusterConfig, err := clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %w", err)
	}
	return clusterConfig, nil
}
