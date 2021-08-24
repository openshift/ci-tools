package main

import (
	"fmt"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func updateSanitizeProwJobs(o options) {
	fmt.Println("Updating sanitize-prow-jobs config")
	filename := filepath.Join(o.releaseRepo, "core-services", "sanitize-prow-jobs", "_config.yaml")
	c := &dispatcher.Config{}
	loadConfig(filename, c)
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
	saveConfig(filename, c)
}
