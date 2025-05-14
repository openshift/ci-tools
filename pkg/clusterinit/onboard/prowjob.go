package onboard

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilpointer "k8s.io/utils/pointer"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	latestImage                      = api.ServiceDomainAPPCIRegistry + "/ci/applyconfig:latest"
	labelRole                        = "ci.openshift.io/role"
	jobRoleInfra                     = "infra"
	generator    jobconfig.Generator = "cluster-init"
)

type prowJobStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *prowJobStep) Name() string { return "prow-jobs" }

func (s *prowJobStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "jobs")
	s.log.Infof("generating: presubmits, postsubmits, and periodics for %s", s.clusterInstall.ClusterName)
	config := prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			"openshift/release": {s.generatePresubmit(s.clusterInstall.ClusterName, *s.clusterInstall.Onboard.OSD, *s.clusterInstall.Onboard.Unmanaged)},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			"openshift/release": {s.generatePostsubmit(s.clusterInstall.ClusterName, *s.clusterInstall.Onboard.OSD, *s.clusterInstall.Onboard.Unmanaged)},
		},
		Periodics: []prowconfig.Periodic{s.generatePeriodic(s.clusterInstall.ClusterName, *s.clusterInstall.Onboard.OSD, *s.clusterInstall.Onboard.Unmanaged)},
	}
	metadata := RepoMetadata()
	jobsDir := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "ci-operator", "jobs")
	return jobconfig.WriteToDir(jobsDir,
		metadata.Org,
		metadata.Repo,
		&config,
		generator,
		map[string]string{jobconfig.LabelBuildFarm: s.clusterInstall.ClusterName})
}

func (s *prowJobStep) generatePeriodic(clusterName string, osd bool, unmanaged bool) prowconfig.Periodic {
	return prowconfig.Periodic{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{s.generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					s.generateContainer("applyconfig:latest",
						clusterName,
						osd, unmanaged,
						[]string{"--confirm=true"},
						nil, nil)},
				ServiceAccountName: ConfigUpdater,
			},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: utilpointer.Bool(true),
				ExtraRefs: []prowapi.Refs{{
					Org:     "openshift",
					Repo:    "release",
					BaseRef: "main",
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

func (s *prowJobStep) generatePostsubmit(clusterName string, osd bool, unmanaged bool) prowconfig.Postsubmit {
	return prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{s.generateSecretVolume(clusterName)},
				Containers: []v1.Container{
					s.generateContainer(latestImage, clusterName, osd, unmanaged, []string{"--confirm=true"}, nil, nil)},
				ServiceAccountName: ConfigUpdater,
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
			Branches: []string{jobconfig.ExactlyBranch("main")},
		},
	}
}

func (s *prowJobStep) generatePresubmit(clusterName string, osd bool, unmanaged bool) prowconfig.Presubmit {
	var optional bool
	if clusterName == string(api.ClusterVSphere02) {
		optional = true
	}
	return prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Name:       RepoMetadata().JobName(jobconfig.PresubmitPrefix, clusterName+"-dry"),
			Agent:      string(prowapi.KubernetesAgent),
			Cluster:    string(api.ClusterAPPCI),
			SourcePath: "",
			Spec: &v1.PodSpec{
				Volumes: []v1.Volume{s.generateSecretVolume(clusterName),
					{
						Name: "tmp",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					}},
				Containers: []v1.Container{
					s.generateContainer(latestImage,
						clusterName,
						osd, unmanaged,
						nil,
						[]v1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}, []v1.EnvVar{{Name: "HOME", Value: "/tmp"}})},
				ServiceAccountName: ConfigUpdater,
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
			Branches: []string{jobconfig.ExactlyBranch("main"), jobconfig.FeatureBranch("main")},
		},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/build-farm/%s-dry", clusterName),
		},
	}
}

func (s *prowJobStep) generateSecretVolume(clusterName string) v1.Volume {
	return v1.Volume{
		Name: "build-farm-credentials",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: "config-updater",
				Items: []v1.KeyToPath{
					{
						Key:  ServiceAccountKubeconfigPath(ConfigUpdater, clusterName),
						Path: "kubeconfig",
					},
				},
			},
		},
	}
}

func (s *prowJobStep) generateContainer(image, clusterName string, osd bool, unmanaged bool, extraArgs []string, extraVolumeMounts []v1.VolumeMount, extraEnvVars []v1.EnvVar) v1.Container {
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

func NewProwJobStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *prowJobStep {
	return &prowJobStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
