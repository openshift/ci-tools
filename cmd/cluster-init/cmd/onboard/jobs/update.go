package jobs

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	latestImage                      = api.ServiceDomainAPPCIRegistry + "/ci/applyconfig:latest"
	labelRole                        = "ci.openshift.io/role"
	jobRoleInfra                     = "infra"
	generator    jobconfig.Generator = "cluster-init"
)

type Options struct {
	ClusterName string
	ReleaseRepo string
	Unmanaged   bool
}

func UpdateJobs(log *logrus.Entry, o Options, osdClusters []string) error {
	log = log.WithField("step", "jobs")
	log.Infof("generating: presubmits, postsubmits, and periodics for %s", o.ClusterName)
	osdClustersSet := sets.NewString(osdClusters...)
	config := prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			"openshift/release": {generatePresubmit(o.ClusterName, osdClustersSet.Has(o.ClusterName), o.Unmanaged)},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			"openshift/release": {generatePostsubmit(o.ClusterName, osdClustersSet.Has(o.ClusterName), o.Unmanaged)},
		},
		Periodics: []prowconfig.Periodic{generatePeriodic(o.ClusterName, osdClustersSet.Has(o.ClusterName), o.Unmanaged)},
	}
	metadata := onboard.RepoMetadata()
	jobsDir := filepath.Join(o.ReleaseRepo, "ci-operator", "jobs")
	return jobconfig.WriteToDir(jobsDir,
		metadata.Org,
		metadata.Repo,
		&config,
		generator,
		map[string]string{jobconfig.LabelBuildFarm: o.ClusterName})
}

func generatePeriodic(clusterName string, osd bool, unmanaged bool) prowconfig.Periodic {
	return prowconfig.Periodic{
		JobBase: prowconfig.JobBase{
			Name:       onboard.RepoMetadata().SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					generateContainer("applyconfig:latest",
						clusterName,
						osd, unmanaged,
						[]string{"--confirm=true"},
						nil, nil)},
				ServiceAccountName: onboard.ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.Bool(true),
				ExtraRefs: []prowapi.Refs{{
					Org:     "openshift",
					Repo:    "release",
					BaseRef: "master",
				}},
			},
			Labels: map[string]string{
				labelRole:                jobRoleInfra,
				jobconfig.LabelBuildFarm: clusterName,
			},
		},
		Interval: "12h",
	}
}

func generatePostsubmit(clusterName string, osd bool, unmanaged bool) prowconfig.Postsubmit {
	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:       onboard.RepoMetadata().JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					generateContainer(latestImage, clusterName, osd, unmanaged, []string{"--confirm=true"}, nil, nil)},
				ServiceAccountName: onboard.ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.Bool(true),
			},
			MaxConcurrency: 1,
			Labels: map[string]string{
				labelRole:                jobRoleInfra,
				jobconfig.LabelBuildFarm: clusterName,
			},
		},
		Brancher: prowconfig.Brancher{
			Branches: []string{jobconfig.ExactlyBranch("master")},
		},
	}
}

func generatePresubmit(clusterName string, osd bool, unmanaged bool) prowconfig.Presubmit {
	var optional bool
	if clusterName == string(api.ClusterVSphere02) {
		optional = true
	}
	return prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:       onboard.RepoMetadata().JobName(jobconfig.PresubmitPrefix, clusterName+"-dry"),
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
						osd, unmanaged,
						nil,
						[]v1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}, []v1.EnvVar{{Name: "HOME", Value: "/tmp"}})},
				ServiceAccountName: onboard.ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{Decorate: utilpointer.Bool(true)},
			Labels: map[string]string{
				jobconfig.CanBeRehearsedLabel: "true",
				jobconfig.LabelBuildFarm:      clusterName,
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
						Key:  onboard.ServiceAccountKubeconfigPath(onboard.ConfigUpdater, clusterName),
						Path: "kubeconfig",
					},
				},
			},
		},
	}
}

func generateContainer(image, clusterName string, osd bool, unmanaged bool, extraArgs []string, extraVolumeMounts []v1.VolumeMount, extraEnvVars []v1.EnvVar) v1.Container {
	var env []v1.EnvVar
	env = append(env, extraEnvVars...)
	if !osd && !unmanaged {
		env = append(env, v1.EnvVar{
			Name: clusterName + "_id",
			ValueFrom: &v1.EnvVarSource{
				SecretKeyRef: &v1.SecretKeySelector{
					Key: clusterName + "-id",
					LocalObjectReference: v1.LocalObjectReference{
						Name: clusterName + "-dex-oidc",
					},
				},
			},
		})
	}
	if clusterName == string(api.ClusterBuild01) || clusterName == string(api.ClusterBuild02) {
		env = append(env, []v1.EnvVar{
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
	if clusterName == string(api.ClusterVSphere02) {
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
