package main

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	LatestImage   = "registry.ci.openshift.org/ci/applyconfig:" + api.LatestReleaseName
	OpenshiftRole = "ci.openshift.io/role"
	Ci            = "ci"
	Infra         = "infra"
	BuildFarm     = "build-farm"
	Tmp           = "tmp"
)

func generatePeriodic(clusterName string) prowconfig.Periodic {
	args := generateArgs(clusterName)
	args = append(args, "--confirm=true")
	return prowconfig.Periodic{
		JobBase: prowconfig.JobBase{
			Name:       "periodic-openshift-release-master-" + clusterName + "-apply",
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
				Decorate: proto.Bool(true),
				ExtraRefs: []prowapi.Refs{{
					Org:     "openshift",
					Repo:    "release",
					BaseRef: "master",
				}},
			},
			Labels: map[string]string{
				OpenshiftRole: Infra,
			},
		},
		Interval: "12h",
	}
}

func generatePostsubmit(clusterName string) prowconfig.Postsubmit {
	args := generateArgs(clusterName)
	args = append(args, "--confirm=true")
	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:       "branch-ci-openshift-release-master-" + clusterName + "-apply",
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes:            []v1.Volume{generateSecretVolume(clusterName)},
				Containers:         []v1.Container{generateContainer(LatestImage, args, generateVolumeMounts())},
				ServiceAccountName: ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: proto.Bool(true),
			},
			MaxConcurrency: 1,
			Labels: map[string]string{
				OpenshiftRole: Infra,
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
			Name:       "pull-ci-openshift-release-master-" + clusterName + "-dry",
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
		Decorate:  proto.Bool(true),
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
