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
	"k8s.io/test-infra/prow/kube"
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
func LoadKubeConfigs(kubeconfig, kubeconfigDir string, kubeconfigChangedCallBack func(fsnotify.Event)) (map[string]*rest.Config, error) {
	var errs []error
	configs, err := kube.LoadClusterConfigs(kube.NewConfig(kube.ConfigFile(kubeconfig),
		kube.ConfigDir(kubeconfigDir), kube.NoInClusterConfig(true)))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to load cluster configs: %w", err))
	}
	ret := map[string]*rest.Config{}
	for k := range configs {
		v := configs[k]
		ret[k] = &v
	}

	var watchFiles []string
	if kubeconfig == "" && kubeconfigDir == "" {
		if kubeconfigsFromEnv := strings.Split(os.Getenv("KUBECONFIG"), ":"); len(kubeconfigsFromEnv) > 0 {
			watchFiles = append(watchFiles, kubeconfigsFromEnv...)
			if len(kubeconfigsFromEnv) > len(ret) {
				errs = append(errs, fmt.Errorf("KUBECONFIG env var with value %s had %d elements but only got %d kubeconfigs", os.Getenv("KUBECONFIG"), len(kubeconfigsFromEnv), len(ret)))
			}
		}
	}

	if kubeconfig != "" {
		watchFiles = append(watchFiles, kubeconfig)
	}
	if kubeconfigDir != "" {
		watchFiles = append(watchFiles, kubeconfigDir)
	}

	if kubeconfigChangedCallBack != nil {
		if err := WatchFiles(watchFiles, kubeconfigChangedCallBack); err != nil {
			errs = append(errs, fmt.Errorf("failed to watch kubeconfigs: %w", err))
		}
	}

	return ret, utilerrors.NewAggregate(errs)
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
