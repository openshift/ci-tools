package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/jobconfig"
)

type InfraPeriodics struct {
	Periodics []prowconfig.Periodic `json:"periodics,omitempty"`
}

func updateJobs(o options) error {
	var config prowconfig.JobConfig
	updatePresubmits(o, &config)
	updatePostsubmits(o, &config)
	updateInfraPeriodics(o, &config)

	metadata := RepoMetadata()
	return jobconfig.WriteToDir(filepath.Join(o.releaseRepo, "ci-operator", "jobs"), metadata.Org, metadata.Repo, &config)
}

func updatePresubmits(o options, config *prowconfig.JobConfig) {
	config.PresubmitsStatic = map[string][]prowconfig.Presubmit{
		"openshift/release": {generatePresubmit(o.clusterName)},
	}
}

func updatePostsubmits(o options, config *prowconfig.JobConfig) {
	config.PostsubmitsStatic = map[string][]prowconfig.Postsubmit{
		"openshift/release": {generatePostsubmit(o.clusterName)},
	}
}

func updateInfraPeriodics(o options, config *prowconfig.JobConfig) {
	config.Periodics = []prowconfig.Periodic{generatePeriodic(o.clusterName)}
}

func periodicExistsFor(o options) (bool, error) {
	metadata := RepoMetadata()
	ipFile := filepath.Join(o.releaseRepo, "ci-operator", "jobs", metadata.JobFilePath("periodics"))
	data, err := ioutil.ReadFile(ipFile)
	if err != nil {
		return true, err
	}
	var ip InfraPeriodics
	if err = yaml.Unmarshal(data, &ip); err != nil {
		return true, err
	}
	_, perErr := findPeriodic(&ip, metadata.SimpleJobName(jobconfig.PeriodicPrefix, o.clusterName+"-apply"))
	return perErr == nil, nil
}

func findPeriodic(ip *InfraPeriodics, name string) (*prowconfig.Periodic, error) {
	idx := -1
	for i, p := range ip.Periodics {
		if name == p.Name {
			idx = i
		}
	}
	if idx != -1 {
		return &ip.Periodics[idx], nil
	}
	return nil, fmt.Errorf("couldn't find periodic with name: %s", name)
}
