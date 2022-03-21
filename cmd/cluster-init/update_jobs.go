package main

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	latestImage                      = api.ServiceDomainAPPCIRegistry + "/ci/applyconfig:latest"
	labelRole                        = "ci.openshift.io/role"
	jobRoleInfra                     = "infra"
	generator    jobconfig.Generator = "cluster-init"
)

func updateJobs(o options) error {
	logrus.Infof("generating: presubmits, postsubmits, and periodics for %s", o.clusterName)
	config := prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			"openshift/release": {generatePresubmit(o.clusterName)},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			"openshift/release": {generatePostsubmit(o.clusterName)},
		},
		Periodics: []prowconfig.Periodic{generatePeriodic(o.clusterName)},
	}
	metadata := RepoMetadata()
	jobsDir := filepath.Join(o.releaseRepo, "ci-operator", "jobs")
	return jobconfig.WriteToDir(jobsDir,
		metadata.Org,
		metadata.Repo,
		&config,
		generator,
		map[string]string{jobconfig.LabelCluster: o.clusterName})
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
						nil, nil)},
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
					generateContainer(latestImage, clusterName, []string{"--confirm=true"}, nil, nil)},
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
			Branches: []string{jobconfig.ExactlyBranch("master")},
		},
	}
}

func generatePresubmit(clusterName string) prowconfig.Presubmit {
	var optional bool
	if clusterName == string(api.ClusterVSphere) {
		optional = true
	}
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
						[]v1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}, []v1.EnvVar{{Name: "HOME", Value: "/tmp"}})},
				ServiceAccountName: configUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{Decorate: utilpointer.BoolPtr(true)},
			Labels: map[string]string{
				jobconfig.CanBeRehearsedLabel: "true",
				jobconfig.LabelCluster:        clusterName,
			},
		},
		AlwaysRun:    false,
		Optional:     optional,
		Trigger:      prowconfig.DefaultTriggerFor(clusterName + "-dry"),
		RerunCommand: prowconfig.DefaultRerunCommandFor(clusterName + "-dry"),
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged: "^clusters/.*",
		},
		Brancher: prowconfig.Brancher{
			Branches: []string{jobconfig.ExactlyBranch("master"), jobconfig.FeatureBranch("master")},
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
				SecretName: "config-updater",
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

func generateContainer(image, clusterName string, extraArgs []string, extraVolumeMounts []v1.VolumeMount, extraEnvVars []v1.EnvVar) v1.Container {
	var env []v1.EnvVar
	env = append(env, extraEnvVars...)
	if clusterName == string(api.ClusterBuild01) || clusterName == string(api.ClusterBuild02) {
		env = append(env, []v1.EnvVar{
			{
				Name: clusterName + "_id",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						Key: clusterName + "-id",
						LocalObjectReference: v1.LocalObjectReference{
							Name: clusterName + "-dex-oidc",
						},
					},
				},
			},
			{
				Name: "slack_api_url",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						Key: "url",
						LocalObjectReference: v1.LocalObjectReference{
							Name: "ci-slack-api-url",
						},
					},
				},
			},
		}...)
	}
	if clusterName == string(api.ClusterVSphere) {
		env = append(env, v1.EnvVar{
			Name: "github_client_id",
			ValueFrom: &v1.EnvVarSource{
				SecretKeyRef: &v1.SecretKeySelector{
					Key: clusterName + "_github_client_id",
					LocalObjectReference: v1.LocalObjectReference{
						Name: "build-farm-credentials",
					},
				},
			},
		})
	}
	return v1.Container{
		Name:    "",
		Image:   image,
		Command: []string{"applyconfig"},
		Args: append([]string{
			fmt.Sprintf("--config-dir=clusters/build-clusters/%s", clusterName),
			"--as=",
			"--kubeconfig=/etc/build-farm-credentials/kubeconfig"},
			extraArgs...),
		Env: env,
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
