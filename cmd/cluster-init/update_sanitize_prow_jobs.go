package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func updateSanitizeProwJobs(o options) error {
	logrus.Println("Updating sanitize-prow-jobs config")
	filename := filepath.Join(o.releaseRepo, "core-services", "sanitize-prow-jobs", "_config.yaml")
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	c := &dispatcher.Config{}
	if err = yaml.Unmarshal(data, c); err != nil {
		return err
	}
	appGroup := c.Groups[api.ClusterAPPCI]
	jobs := appGroup.Jobs
	jobs = append(jobs, fmt.Sprintf("pull-ci-openshift-release-master-%s-dry", o.clusterName))
	jobs = append(jobs, fmt.Sprintf("branch-ci-openshift-release-master-%s-apply", o.clusterName))
	jobs = append(jobs, fmt.Sprintf("periodic-openshift-release-master-%s-apply", o.clusterName))
	c.Groups[api.ClusterAPPCI] = dispatcher.Group{
		Jobs:    jobs,
		Paths:   appGroup.Paths,
		PathREs: appGroup.PathREs,
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
