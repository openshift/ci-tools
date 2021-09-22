package main

import (
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

func updateSanitizeProwJobs(o options) error {
	logrus.Info("Updating sanitize-prow-jobs config")
	filename := filepath.Join(o.releaseRepo, "core-services", "sanitize-prow-jobs", "_config.yaml")
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	var c dispatcher.Config
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	updateSanitizeProwJobsConfig(&c, o.clusterName)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, rawYaml, 0644)
}

func updateSanitizeProwJobsConfig(c *dispatcher.Config, clusterName string) {
	appGroup := c.Groups[api.ClusterAPPCI]
	metadata := RepoMetadata()
	appGroup.Jobs = sets.NewString(appGroup.Jobs...).
		Insert(metadata.JobName(jobconfig.PresubmitPrefix, clusterName+"-dry")).
		Insert(metadata.JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply")).
		Insert(metadata.SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply")).
		List()
	c.Groups[api.ClusterAPPCI] = appGroup
}
