package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"
)

const (
	IPFilePath      = "ci-operator/jobs/infra-periodics.yaml"
	PrePostFilePath = "ci-operator/jobs/openshift/release"
)

type InfraPeriodics struct {
	Periodics []prowconfig.Periodic `json:"periodics,omitempty"`
}

type Post struct {
	OSRelease struct {
		Jobs []prowconfig.Postsubmit `json:"openshift/release,omitempty"`
	} `json:"postsubmits,omitempty"`
}

type Pre struct {
	OSRelease struct {
		Jobs []prowconfig.Presubmit `json:"openshift/release,omitempty"`
	} `json:"presubmits,omitempty"`
}

func updatePresubmits(o options) error {
	presubmitsFile := filepath.Join(o.releaseRepo, PrePostFilePath, "openshift-release-master-presubmits.yaml")
	logrus.Infof("Updating Presubmit Jobs: %s", presubmitsFile)
	data, err := ioutil.ReadFile(presubmitsFile)
	if err != nil {
		return err
	}
	var presubmits Pre
	if err = yaml.Unmarshal(data, &presubmits); err != nil {
		return err
	}

	presubmit := generatePresubmit(o.clusterName)
	presubmits.OSRelease.Jobs = append(presubmits.OSRelease.Jobs, presubmit)

	yamlConfig, err := yaml.Marshal(presubmits)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(presubmitsFile, yamlConfig, 0644)
}

func updatePostsubmits(o options) error {
	postsubmitsFile := filepath.Join(o.releaseRepo, PrePostFilePath, "openshift-release-master-postsubmits.yaml")
	logrus.Infof("Updating Postsubmit Jobs: %s", postsubmitsFile)
	data, err := ioutil.ReadFile(postsubmitsFile)
	if err != nil {
		return err
	}
	var postsubmits Post
	if err = yaml.Unmarshal(data, &postsubmits); err != nil {
		return err
	}

	postsubmit := generatePostsubmit(o.clusterName)
	postsubmits.OSRelease.Jobs = append(postsubmits.OSRelease.Jobs, postsubmit)

	yamlConfig, err := yaml.Marshal(postsubmits)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(postsubmitsFile, yamlConfig, 0644)
}

func updateInfraPeriodics(o options) error {
	ipFile := filepath.Join(o.releaseRepo, IPFilePath)
	logrus.Infof("Updating Periodic Jobs: %s", ipFile)
	data, err := ioutil.ReadFile(ipFile)
	if err != nil {
		return err
	}
	var ip InfraPeriodics
	if err := yaml.Unmarshal(data, &ip); err != nil {
		return err
	}

	ap := generatePeriodic(o.clusterName)
	ip.Periodics = append(ip.Periodics, ap)

	yamlConfig, err := yaml.Marshal(ip)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(ipFile, yamlConfig, 0644)
}

func periodicExistsFor(o options) (bool, error) {
	ipFile := filepath.Join(o.releaseRepo, IPFilePath)
	data, err := ioutil.ReadFile(ipFile)
	if err != nil {
		return true, err
	}
	var ip InfraPeriodics
	if err = yaml.Unmarshal(data, &ip); err != nil {
		return true, err
	}
	_, perErr := findPeriodic(&ip, periodicFor(o.clusterName))
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

func periodicFor(clusterName string) string {
	return fmt.Sprintf("periodic-openshift-release-master-%s-apply", clusterName)
}
