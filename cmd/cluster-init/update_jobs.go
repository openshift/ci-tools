package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	latestImage                      = api.ServiceDomainAPPCIRegistry + "/ci/applyconfig:latest"
	labelRole                        = "ci.openshift.io/role"
	jobRoleInfra                     = "infra"
	generator    jobconfig.Generator = "cluster-init"
)

type BuildClusters struct {
	Managed []string `json:"managed,omitempty"`
}

func updateJobs(o options) error {
	var clusters []string
	if o.clusterName == "" {
		// Updating ALL cluster-init managed clusters
		buildClusters, err := loadBuildClusters(o)
		if err != nil {
			return err
		}
		clusters = buildClusters.Managed
	} else {
		clusters = []string{o.clusterName}
	}
	config := prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
		Periodics:         []prowconfig.Periodic{},
	}
	for _, cluster := range clusters {
		logrus.Infof("updating presubmits, postsubmits, and periodics for cluster: %s", cluster)
		config.Periodics = append(config.Periodics, generatePeriodic(cluster))
		config.PostsubmitsStatic["openshift/release"] = append(config.PostsubmitsStatic["openshift/release"], generatePostsubmit(cluster))
		config.PresubmitsStatic["openshift/release"] = append(config.PresubmitsStatic["openshift/release"], generatePresubmit(cluster))
	}

	metadata := RepoMetadata()
	jobsDir := filepath.Join(o.releaseRepo, "ci-operator", "jobs")
	var matchLabels map[string]string
	if o.clusterName != "" {
		// We are only updating a single cluster, so we must only match on that
		matchLabels = map[string]string{jobconfig.LabelCluster: o.clusterName}
	}
	return jobconfig.WriteToDir(jobsDir, metadata.Org, metadata.Repo, &config, generator, matchLabels)
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
	return filepath.Join(o.releaseRepo, "clusters", "build-clusters", "cluster-init.yaml")
}

func generatePeriodic(clusterName string) prowconfig.Periodic {
	return prowconfig.Periodic{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					generateContainer("applyconfig:latest",
						clusterName,
						[]string{"--confirm=true"},
						nil)},
				ServiceAccountName: configUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.BoolPtr(true),
				ExtraRefs: []prowapi.Refs{{
					Org:     "openshift",
					Repo:    "release",
					BaseRef: "master",
				}},
			},
			Labels: map[string]string{
				labelRole:              jobRoleInfra,
				jobconfig.LabelCluster: clusterName,
			},
		},
		Interval: "12h",
	}
}

func generatePostsubmit(clusterName string) prowconfig.Postsubmit {
	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					generateContainer(latestImage, clusterName, []string{"--confirm=true"}, nil)},
				ServiceAccountName: configUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.BoolPtr(true),
			},
			MaxConcurrency: 1,
			Labels: map[string]string{
				labelRole:              jobRoleInfra,
				jobconfig.LabelCluster: clusterName,
			},
		},
		Brancher: prowconfig.Brancher{
			Branches: []string{"master"},
		},
	}
}

func generatePresubmit(clusterName string) prowconfig.Presubmit {
	return prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PresubmitPrefix, clusterName+"-dry"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName),
					{
						Name: "tmp",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					}},
				Containers: []v1.Container{
					generateContainer(latestImage,
						clusterName,
						nil,
						[]v1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}})},
				ServiceAccountName: configUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{Decorate: utilpointer.BoolPtr(true)},
			Labels: map[string]string{
				jobconfig.CanBeRehearsedLabel: "true",
				jobconfig.LabelCluster:        clusterName,
			},
		},
		AlwaysRun:    true,
		Optional:     false,
		Trigger:      prowconfig.DefaultTriggerFor(clusterName),
		RerunCommand: prowconfig.DefaultRerunCommandFor(clusterName + "-dry"),
		Brancher: prowconfig.Brancher{
			Branches: []string{"master"},
		},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/build-farm/%s-dry", clusterName),
		},
	}
}

func generateSecretVolume(clusterName string) v1.Volume {
	return v1.Volume{
		Name: "build-farm-credentials",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: "build-farm-credentials",
				Items: []v1.KeyToPath{
					{
						Key:  serviceAccountKubeconfigPath(configUpdater, clusterName),
						Path: "kubeconfig",
					},
				},
			},
		},
	}
}

func generateContainer(image, clusterName string, extraArgs []string, extraVolumeMounts []v1.VolumeMount) v1.Container {
	return v1.Container{
		Name:    "",
		Image:   image,
		Command: []string{"applyconfig"},
		Args: append([]string{
			fmt.Sprintf("--config-dir=clusters/build-clusters/%s", clusterName),
			"--as=",
			"--KUBECONFIG=/etc/build-farm-credentials/KUBECONFIG"},
			extraArgs...),
		Resources: v1.ResourceRequirements{
			Requests: map[v1.ResourceName]resource.Quantity{"cpu": resource.MustParse("10m")},
		},
		ImagePullPolicy: "Always",
		VolumeMounts: append([]v1.VolumeMount{{
			Name:      "build-farm-credentials",
			ReadOnly:  true,
			MountPath: "/etc/build-farm-credentials"}},
			extraVolumeMounts...),
	}
}
