package clustermgmt

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

func LoadClusterInstall(path string) (*ClusterInstall, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %v", path, err)
	}
	ci := &ClusterInstall{}
	if err := yaml.Unmarshal(b, &ci); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %v", path, err)
	}
	return ci, nil
}
