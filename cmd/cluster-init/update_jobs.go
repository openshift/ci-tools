package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
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
	logrus.Infof("Updating Presubmit Jobs: %s\n", presubmitsFile)
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
	logrus.Infof("Updating Postsubmit Jobs: %s\n", postsubmitsFile)
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
	logrus.Infof("Updating Periodic Jobs: %s\n", ipFile)
	data, err := ioutil.ReadFile(ipFile)
	if err != nil {
		return err
	}
	var ip InfraPeriodics
	if err := yaml.Unmarshal(data, &ip); err != nil {
		return err
	}

	rotSASecretsPer, err := findPeriodic(&ip, "periodic-rotate-serviceaccount-secrets")
	if err != nil {
		return err
	}
	if err = appendNewClustersConfigUpdaterToKubeconfig(rotSASecretsPer, "", o.clusterName); err != nil {
		return err
	}
	if err = appendBuildFarmCredentialSecret(rotSASecretsPer, o.clusterName); err != nil {
		return err
	}
	for _, perName := range []string{"periodic-ci-secret-generator", "periodic-ci-secret-bootstrap"} {
		per, err := findPeriodic(&ip, perName)
		if err != nil {
			return err
		}
		if err = appendNewClustersConfigUpdaterToKubeconfig(per, "ci-secret-bootstrap", o.clusterName); err != nil {
			return err
		}
		if err = appendBuildFarmCredentialSecret(per, o.clusterName); err != nil {
			return err
		}
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
	_, perErr := findPeriodic(&ip, fmt.Sprintf("periodic-openshift-release-master-%s-apply", o.clusterName))
	return perErr == nil, nil
}

func appendNewClustersConfigUpdaterToKubeconfig(per *prowconfig.Periodic, containerName, clusterName string) error {
	container, err := findContainer(per.Spec, containerName)
	if err != nil {
		return err
	}
	env, err := findEnv(container, Kubeconfig)
	if err != nil {
		return err
	}
	env.Value = env.Value + fmt.Sprintf(":/etc/build-farm-credentials/%s", serviceAccountKubeconfigPath(ConfigUpdater, clusterName))
	return nil
}

func appendBuildFarmCredentialSecret(per *prowconfig.Periodic, clusterName string) error {
	v, err := findVolume(per.Spec, "build-farm-credentials")
	if err != nil {
		return err
	}
	configPath := serviceAccountKubeconfigPath(ConfigUpdater, clusterName)
	path := v1.KeyToPath{
		Key:  configPath,
		Path: configPath,
	}
	v.Secret.Items = append(v.Secret.Items, path)
	return nil
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

func findContainer(ps *v1.PodSpec, name string) (*v1.Container, error) {
	idx := -1
	for i, c := range ps.Containers {
		if c.Name == name {
			idx = i
		}
	}
	if idx != -1 {
		return &ps.Containers[idx], nil
	}
	return nil, fmt.Errorf("couldn't find Container with name: %s", name)
}

func findEnv(c *v1.Container, name string) (*v1.EnvVar, error) {
	idx := -1
	for i, e := range c.Env {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx != -1 {
		return &c.Env[idx], nil
	}
	return nil, fmt.Errorf("couldn't find Env with name: %s", name)
}

func findVolume(ps *v1.PodSpec, name string) (*v1.Volume, error) {
	idx := -1
	for i, v := range ps.Volumes {
		if v.Name == name {
			idx = i
			break
		}
	}
	if idx != -1 {
		return &ps.Volumes[idx], nil
	}
	return nil, fmt.Errorf("couldn't find Volume with name: %s", name)
}
