package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
)

//SecretGenConfig is used here as using secretgenerator.Config results in 'special' unmarshalling
//where '$(*)' wildcards from the yaml are expanded in the output. Doing so for this purpose results in
//incorrect re-serialization
type SecretGenConfig []secretgenerator.SecretItem

func updateSecretGenerator(o options) error {
	filename := filepath.Join(o.releaseRepo, "core-services", "ci-secret-generator", "_config.yaml")
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	c := &SecretGenConfig{}
	if err = yaml.Unmarshal(data, c); err != nil {
		return err
	}
	if err := appendToSecretItem(BuildUFarm, "sa.$(service_account).$(cluster).config", o, c); err != nil {
		return err
	}
	if err := appendToSecretItem(BuildUFarm, "token_image-puller_$(cluster)_reg_auth_value.txt", o, c); err != nil {
		return err
	}
	if err := appendToSecretItem("ci-chat-bot", "sa.$(service_account).$(cluster).config", o, c); err != nil {
		return err
	}
	if err := appendToSecretItem(PodScaler, "sa.$(service_account).$(cluster).config", o, c); err != nil {
		return err
	}

	y, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err = ioutil.WriteFile(filename, y, 0644); err != nil {
		return err
	}

	return nil
}

func appendToSecretItem(itemName string, name string, o options, c *SecretGenConfig) error {
	si, err := findSecretItem(itemName, name, string(api.ClusterBuild01), *c)
	if err != nil {
		return err
	}
	logrus.Printf("Appending to secret item: {itemName: %s, name: %s, likeCluster: %s}\n", itemName, name, string(api.ClusterBuild01))
	si.Params["cluster"] = append(si.Params["cluster"], o.clusterName)
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
				}
			}
			for _, cluster := range si.Params["cluster"] {
				if likeCluster == cluster {
					containsCluster = true
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
	return &secretgenerator.SecretItem{},
		fmt.Errorf("couldn't find SecretItem with item_name: %s name: %s containing cluster: %v",
			itemName,
			name,
			likeCluster)
}
