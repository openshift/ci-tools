package main

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	LatestImage = api.ServiceDomainAPPCIRegistry + "/ci/applyconfig:latest"
	LabelRole   = "ci.openshift.io/role"
	Infra       = "infra"
)

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
					generateContainer(fmt.Sprintf("%s:%s", "applyconfig", api.LatestReleaseName),
						clusterName,
						[]string{"--confirm=true"},
						[]v1.VolumeMount{})},
				ServiceAccountName: ConfigUpdater,
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
				LabelRole: Infra,
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
					generateContainer(LatestImage, clusterName, []string{"--confirm=true"}, []v1.VolumeMount{})},
				ServiceAccountName: ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.BoolPtr(true),
			},
			MaxConcurrency: 1,
			Labels: map[string]string{
				LabelRole: Infra,
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
					generateContainer(LatestImage,
						clusterName,
						[]string{},
						[]v1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}})},
				ServiceAccountName: ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{Decorate: utilpointer.BoolPtr(true)},
			Labels: map[string]string{
				"pj-rehearse.openshift.io/can-be-rehearsed": "true",
			},
		},
		AlwaysRun:    true,
		Optional:     false,
		Trigger:      prowconfig.DefaultTriggerFor(clusterName),
		RerunCommand: prowconfig.DefaultRerunCommandFor(clusterName) + "-dry",
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
						Key:  serviceAccountKubeconfigPath(ConfigUpdater, clusterName),
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
