package util

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

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
func LoadKubeConfigs(kubeconfig string, kubeconfigChangedCallBack func(fsnotify.Event)) (map[string]*rest.Config, string, error) {
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
		logrus.Debugf("Parsed kubeconfig context: %s", context)
	}
	if kubeconfigsFromEnv := strings.Split(os.Getenv("KUBECONFIG"), ":"); len(kubeconfigsFromEnv) > 0 && len(kubeconfigsFromEnv) > len(configs) {
		errs = append(errs, fmt.Errorf("KUBECONFIG env var with value %s had %d elements but only got %d kubeconfigs", os.Getenv("KUBECONFIG"), len(kubeconfigsFromEnv), len(configs)))
	}

	if kubeconfigChangedCallBack != nil {
		if err := WatchFiles(append(loader.Precedence, kubeconfig), kubeconfigChangedCallBack); err != nil {
			errs = append(errs, fmt.Errorf("failed to watch kubeconfigs: %w", err))
		}
	}
	return configs, cfg.CurrentContext, utilerrors.NewAggregate(errs)
}

// WatchFiles watches the passed files if they exist and calls callback for all events
// except Chmod, as Openshift seems to generate frequent Chmod events
func WatchFiles(candidates []string, callback func(fsnotify.Event)) error {
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
			callback(event)
		}
	}()

	return nil
}
