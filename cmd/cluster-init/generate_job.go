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
	Tmp         = "tmp"
)

func generatePeriodic(clusterName string) prowconfig.Periodic {
	args := append(generateArgs(clusterName), "--confirm=true")
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
						args,
						generateVolumeMounts())},
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
	args := append(generateArgs(clusterName), "--confirm=true")
	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes:            []v1.Volume{generateSecretVolume(clusterName)},
				Containers:         []v1.Container{generateContainer(LatestImage, args, generateVolumeMounts())},
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
	mounts := generateVolumeMounts()
	mounts = append(mounts, v1.VolumeMount{
		Name:      Tmp,
		MountPath: "/" + Tmp,
	})
	return prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PresubmitPrefix, clusterName+"-dry"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName),
					{
						Name: Tmp,
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					}},
				Containers:         []v1.Container{generateContainer(LatestImage, generateArgs(clusterName), mounts)},
				ServiceAccountName: ConfigUpdater,
			},
			UtilityConfig: generateUtilityConfig(),
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

func generateUtilityConfig() prowconfig.UtilityConfig {
	return prowconfig.UtilityConfig{
		Decorate:  utilpointer.BoolPtr(true),
		ExtraRefs: []prowapi.Refs{},
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

func generateContainer(image string, args []string, volumeMounts []v1.VolumeMount) v1.Container {
	return v1.Container{
		Name:    "",
		Image:   image,
		Command: []string{"applyconfig"},
		Args:    args,
		Resources: v1.ResourceRequirements{
			Requests: map[v1.ResourceName]resource.Quantity{
				"cpu": resource.MustParse("10m"),
			},
		},
		ImagePullPolicy: "Always",
		VolumeMounts:    volumeMounts,
	}
}

func generateArgs(clusterName string) []string {
	return []string{
		fmt.Sprintf("--config-dir=clusters/build-clusters/%s", clusterName),
		"--as=",
		"--KUBECONFIG=/etc/build-farm-credentials/KUBECONFIG",
	}
}

func generateVolumeMounts() []v1.VolumeMount {
	return []v1.VolumeMount{
		{
			Name:      "build-farm-credentials",
			ReadOnly:  true,
			MountPath: "/etc/build-farm-credentials",
		},
	}
}
