package main

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

type BuildClusters struct {
	Managed []string `json:"managed,omitempty"`
	Hosted  []string `json:"hosted,omitempty"`
	Osd     []string `json:"osd,omitempty"`
}

func updateBuildClusters(o options) error {
	if o.unmanaged {
		logrus.Infof("skipping build clusters config update for unmanaged cluster: %s", o.clusterName)
		return nil
	}
	logrus.Infof("updating build clusters config to add: %s", o.clusterName)
	buildClusters, err := loadBuildClusters(o)
	if err != nil {
		return err
	}

	buildClusters.Managed = append(buildClusters.Managed, o.clusterName)
	if o.hosted {
		buildClusters.Hosted = append(buildClusters.Hosted, o.clusterName)
	}

	if o.osd {
		buildClusters.Osd = append(buildClusters.Osd, o.clusterName)
	}

	rawYaml, err := yaml.Marshal(buildClusters)
	if err != nil {
		return err
	}
	return os.WriteFile(buildClustersFile(o), rawYaml, 0644)
}

func loadBuildClusters(o options) (*BuildClusters, error) {
	filename := buildClustersFile(o)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var buildClusters BuildClusters
	if err = yaml.Unmarshal(data, &buildClusters); err != nil {
		return nil, err
	}
	return &buildClusters, nil
}

func buildClustersFile(o options) string {
	return filepath.Join(o.releaseRepo, "clusters", "build-clusters", "_cluster-init.yaml")
}
