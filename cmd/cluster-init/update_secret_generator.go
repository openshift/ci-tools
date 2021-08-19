package main

import (
	"fmt"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"path/filepath"
)

const (
	Build01 = "build01"
)

//SecretGenConfig is used here as using secretgenerator.Config results in 'special' unmarshalling
//where '$(*)' wildcards from the yaml are expanded in the output. Doing so for this purpose results in
//incorrect re-serialization
type SecretGenConfig []secretgenerator.SecretItem

func updateSecretGenerator(o options) {
	filename := filepath.Join(o.releaseRepo, "core-services", "ci-secret-generator", "_config.yaml")
	c := &SecretGenConfig{}
	loadConfig(filename, c)
	appendToSecretItem(BuildUFarm, "sa.$(service_account).$(cluster).config", o, c)
	appendToSecretItem(BuildUFarm, "token_image-puller_$(cluster)_reg_auth_value.txt", o, c)
	appendToSecretItem(fmt.Sprintf("%s-%s", Ci, ChatBot), "sa.$(service_account).$(cluster).config", o, c)
	appendToSecretItem(PodScaler, "sa.$(service_account).$(cluster).config", o, c)

	saveConfig(filename, c)
}

func appendToSecretItem(itemName string, name string, o options, c *SecretGenConfig) {
	si, err := findSecretItem(itemName, name, Build01, *c)
	fmt.Printf("Appending to secret item: {itemName: %s, name: %s, likeCluster: %s}\n", itemName, name, Build01)
	check(err)
	si.Params["cluster"] = append(si.Params["cluster"], o.clusterName)
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
