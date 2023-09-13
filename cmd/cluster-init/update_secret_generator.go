package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
)

const (
	serviceAccountWildcard = "$(service_account)"
	clusterWildcard        = "$(cluster)"
)

// SecretGenConfig is used here as using secretgenerator.Config results in 'special' unmarshalling
// where '$(*)' wildcards from the yaml are expanded in the output. Doing so for this purpose results in
// incorrect re-serialization
type SecretGenConfig []secretgenerator.SecretItem

func updateSecretGenerator(o options) error {
	filename := filepath.Join(o.releaseRepo, "core-services", "ci-secret-generator", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c SecretGenConfig
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if err = updateSecretGeneratorConfig(o, &c); err != nil {
		return err
	}
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func updateSecretGeneratorConfig(o options, c *SecretGenConfig) error {
	serviceAccountConfigPath := serviceAccountKubeconfigPath(serviceAccountWildcard, clusterWildcard)
	if err := appendToSecretItem(buildUFarm, serviceAccountConfigPath, o, c); err != nil {
		return err
	}
	if err := appendToSecretItem(buildUFarm, fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", clusterWildcard), o, c); err != nil {
		return err
	}
	if err := appendToSecretItem("ci-chat-bot", serviceAccountConfigPath, o, c); err != nil {
		return err
	}
	if !o.unmanaged {
		if err := appendToSecretItem(podScaler, serviceAccountConfigPath, o, c); err != nil {
			return err
		}
	}
	return nil
}

func appendToSecretItem(itemName string, name string, o options, c *SecretGenConfig) error {
	si, err := findSecretItem(itemName, name, string(api.ClusterBuild01), *c)
	if err != nil {
		return err
	}
	logrus.Infof("Appending to secret item: {itemName: %s, name: %s, likeCluster: %s}", itemName, name, string(api.ClusterBuild01))
	si.Params["cluster"] = sets.List(sets.New[string](si.Params["cluster"]...).Insert(o.clusterName))
	return nil
}

func findSecretItem(itemName string, name string, likeCluster string, c SecretGenConfig) (*secretgenerator.SecretItem, error) {
	idx := -1
	for i, si := range c {
		if itemName == si.ItemName {
			containsName := false
			containsCluster := false
			for _, fj := range si.Fields {
				if name == fj.Name {
					containsName = true
					break
				}
			}
			for _, cluster := range si.Params["cluster"] {
				if likeCluster == cluster {
					containsCluster = true
					break
				}
			}
			if containsName && containsCluster {
				idx = i
				break
			}
		}
	}
	if idx != -1 {
		return &c[idx], nil
	}
	return nil,
		fmt.Errorf("couldn't find SecretItem with item_name: %s name: %s containing cluster: %v",
			itemName,
			name,
			likeCluster)
}
