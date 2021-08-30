package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

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
	c := &dispatcher.Config{}
	if err = yaml.Unmarshal(data, c); err != nil {
		return err
	}
	updateConfig(c, o.clusterName)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, rawYaml, 0644)
}

func updateConfig(c *dispatcher.Config, clusterName string) {
	appGroup := c.Groups[api.ClusterAPPCI]
	jobs := appGroup.Jobs
	metadata := api.Metadata{
		Org:    "openshift",
		Repo:   "release",
		Branch: "master",
	}
	jobs = append(jobs, metadata.JobName(jobconfig.PresubmitPrefix, clusterName+"-dry"))
	jobs = append(jobs, metadata.JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply"))
	jobs = append(jobs, fmt.Sprintf("%s-%s-%s-%s-%s-apply",
		jobconfig.PeriodicPrefix, metadata.Org, metadata.Repo, metadata.Branch, clusterName))
	c.Groups[api.ClusterAPPCI] = dispatcher.Group{
		Jobs:    jobs,
		Paths:   appGroup.Paths,
		PathREs: appGroup.PathREs,
	}
}
