package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/group"
)

func updateSyncRoverGroups(o options) error {
	filename := filepath.Join(o.releaseRepo, "core-services", "sync-rover-groups", "_config.yaml")
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	var c group.Config
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.ClusterGroups == nil {
		return fmt.Errorf("`cluster_groups` is not defined in the sync-rover-groups' configuration")
	}
	c.ClusterGroups["build-farm"] = sets.NewString(c.ClusterGroups["build-farm"]...).Insert(o.clusterName).List()
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, rawYaml, 0644)
}
