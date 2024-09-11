package runtime

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/prow/pkg/kube"
)

type Kubeconfigs struct {
	configs map[string]rest.Config
	admin   *rest.Config
}

func (k *Kubeconfigs) Resolve(clusterName string) (rest.Config, bool) {
	c, found := k.configs[clusterName]
	return c, found
}

func (k *Kubeconfigs) Admin() (rest.Config, bool) {
	if k.admin != nil {
		return *k.admin, true
	}
	return rest.Config{}, false
}

func LoadKubeconfigs(dir, suffix, adminKubeconfig string) (*Kubeconfigs, error) {
	k := Kubeconfigs{}
	if dir != "" && suffix != "" {
		configs, err := kube.LoadClusterConfigs(kube.NewConfig(
			kube.ConfigDir(dir),
			kube.ConfigSuffix(suffix),
			kube.NoInClusterConfig(true)))
		if err != nil {
			return nil, fmt.Errorf("load cluster configs: %v", err)
		}
		k.configs = configs
	}
	if adminKubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", adminKubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load admin config: %v", err)
		}
		k.admin = config
	}
	return &k, nil
}
