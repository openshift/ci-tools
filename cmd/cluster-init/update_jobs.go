package main

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"
)

const (
	IPFilePath      = "ci-operator/jobs/infra-periodics.yaml"
	PrePostFilePath = "ci-operator/jobs/openshift/release"
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

func updatePresubmits(o options) {
	presubmitsFile := filepath.Join(o.releaseRepo, PrePostFilePath, "openshift-release-master-presubmits.yaml")
	fmt.Printf("Updating Presubmit Jobs: %s\n", presubmitsFile)
	presubmits := &Pre{}
	loadConfig(presubmitsFile, presubmits)
	presubmit := generatePresubmit(o.clusterName)
	presubmits.OSRelease.Jobs = append(presubmits.OSRelease.Jobs, presubmit)
	saveConfig(presubmitsFile, presubmits)
}

func updatePostsubmits(o options) {
	postsubmitsFile := filepath.Join(o.releaseRepo, PrePostFilePath, "openshift-release-master-postsubmits.yaml")
	fmt.Printf("Updating Postsubmit Jobs: %s\n", postsubmitsFile)
	postsubmits := &Post{}
	loadConfig(postsubmitsFile, postsubmits)
	postsubmit := generatePostsubmit(o.clusterName)
	postsubmits.OSRelease.Jobs = append(postsubmits.OSRelease.Jobs, postsubmit)
	saveConfig(postsubmitsFile, *postsubmits)
}

func updateInfraPeriodics(o options) {
	ipFile := filepath.Join(o.releaseRepo, IPFilePath)
	fmt.Printf("Updating Periodic Jobs: %s\n", ipFile)
	ip := &InfraPeriodics{}
	loadConfig(ipFile, ip)
	rotSASecretsPer, err := findPeriodic(ip, "periodic-rotate-serviceaccount-secrets")
	check(err)
	appendNewClustersConfigUpdaterToKubeconfig(rotSASecretsPer, "", o.clusterName)
	appendBuildFarmCredentialSecret(rotSASecretsPer, o.clusterName)
	for _, perName := range []string{"periodic-ci-secret-generator", "periodic-ci-secret-bootstrap"} {
		per, err := findPeriodic(ip, perName)
		check(err)
		appendNewClustersConfigUpdaterToKubeconfig(per, "ci-secret-bootstrap", o.clusterName)
		appendBuildFarmCredentialSecret(per, o.clusterName)
	}
	ap := generatePeriodic(o.clusterName)
	ip.Periodics = append(ip.Periodics, ap)
	saveConfig(ipFile, *ip)
}

func periodicExistsFor(o options) bool {
	ipFile := filepath.Join(o.releaseRepo, IPFilePath)
	ip := &InfraPeriodics{}
	loadConfig(ipFile, ip)
	_, err := findPeriodic(ip, fmt.Sprintf("periodic-openshift-release-master-%s-apply", o.clusterName))
	return err == nil
}

func appendNewClustersConfigUpdaterToKubeconfig(per *prowconfig.Periodic, containerName string, clusterName string) {
	container, err := findContainer(per.Spec, containerName)
	if err != nil {
		logrus.WithError(err).Fatal()
	}
	env, err := findEnv(container, Kubeconfig)
	check(err)
	env.Value = env.Value + fmt.Sprintf(":/etc/build-farm-credentials/sa.config-updater.%s.config", clusterName)
}

func appendBuildFarmCredentialSecret(per *prowconfig.Periodic, clusterName string) {
	v, err := findVolume(per.Spec, "build-farm-credentials")
	check(err)
	configPath := secretConfigFor(ConfigUpdater, clusterName)
	path := v1.KeyToPath{
		Key:  configPath,
		Path: configPath,
	}
	v.Secret.Items = append(v.Secret.Items, path)
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
	return &prowconfig.Periodic{}, fmt.Errorf("couldn't find periodic with name: %s", name)
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
	return &v1.Container{}, fmt.Errorf("couldn't find Container with name: %s", name)
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
	return &v1.EnvVar{}, fmt.Errorf("couldn't find Env with name: %s", name)
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
	return &v1.Volume{}, fmt.Errorf("couldn't find Volume with name: %s", name)
}
