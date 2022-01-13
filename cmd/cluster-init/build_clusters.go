package main

import (
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

type BuildClusters struct {
	Managed []string `json:"managed,omitempty"`
}

func updateBuildClusters(o options) error {
	logrus.Infof("updating build clusters config to add: %s", o.clusterName)
	buildClusters, err := loadBuildClusters(o)
	if err != nil {
		return err
	}

	buildClusters.Managed = append(buildClusters.Managed, o.clusterName)

	rawYaml, err := yaml.Marshal(buildClusters)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(buildClustersFile(o), rawYaml, 0644)
}

func loadBuildClusters(o options) (*BuildClusters, error) {
	filename := buildClustersFile(o)
	data, err := ioutil.ReadFile(filename)
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
