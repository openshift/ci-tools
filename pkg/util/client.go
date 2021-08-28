package util

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/test-infra/prow/flagutil"
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

// LoadKubeConfigs loads kubeconfigs. If the kubeconfigChangedCallBack is non-nil, it will watch all kubeconfigs it loaded
// and call the callback once they change.
func LoadKubeConfigs(opts flagutil.KubernetesOptions, kubeconfigChangedCallBack func()) (map[string]*rest.Config, error) {
	configs, err := opts.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster configs: %w", err)
	}
	ret := map[string]*rest.Config{}
	for k := range configs {
		v := configs[k]
		ret[k] = &v
	}
	return ret, nil
}

// WatchFiles watches the passed files if they exist and calls callback for all events
// except Chmod, as Openshift seems to generate frequent Chmod events
func WatchFiles(candidates []string, callback func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to construct watcher: %w", err)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if err := watcher.Add(candidate); err != nil {
			return fmt.Errorf("failed to watch %s: %w", candidate, err)
		}
	}

	go func() {
		for event := range watcher.Events {
			if event.Op == fsnotify.Chmod {
				// For some reason we get frequent chmod events
				continue
			}
			logrus.WithField("event", event.String()).Info("File changed, calling callback")
			callback()
		}
	}()

	return nil
}
