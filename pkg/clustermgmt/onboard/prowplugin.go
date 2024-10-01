package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/yaml"
)

type prowPluginStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *prowPluginStep) Name() string { return "prow-plugin" }

func (s *prowPluginStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "prow-plugin")
	s.log.Info("Updating Prow plugin config")
	filename := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "core-services", "prow", "02_config", "_plugins.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c plugins.Configuration
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	s.updateProwPluginConfigConfigUpdater(&c, s.clusterInstall.ClusterName)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func (s *prowPluginStep) updateProwPluginConfigConfigUpdater(c *plugins.Configuration, clusterName string) {
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

func NewProwPluginStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *prowPluginStep {
	return &prowPluginStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
