package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"
)

func updateProwPluginConfig(o options) error {
	logrus.Info("Updating Prow plugin config")
	filename := filepath.Join(o.releaseRepo, "core-services", "prow", "02_config", "_plugins.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c plugins.Configuration
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	updateProwPluginConfigConfigUpdater(&c, o.clusterName)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func updateProwPluginConfigConfigUpdater(c *plugins.Configuration, clusterName string) {
	if c.ConfigUpdater.ClusterGroups == nil {
		c.ConfigUpdater.ClusterGroups = map[string]plugins.ClusterGroup{}
	}
	for _, ns := range []string{"ci", "ocp"} {
		clusters := sets.New[string](clusterName)
		namespaces := sets.New[string](ns)
		key := fmt.Sprintf("build_farm_%s", ns)
		if gc, ok := c.ConfigUpdater.ClusterGroups[key]; ok {
			clusters = clusters.Union(sets.New[string](gc.Clusters...))
			namespaces = namespaces.Union(sets.New[string](gc.Namespaces...))
		}
		c.ConfigUpdater.ClusterGroups[key] = plugins.ClusterGroup{Clusters: sets.List(clusters), Namespaces: sets.List(namespaces)}
	}
}
