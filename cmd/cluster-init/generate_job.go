package main

import (
	"fmt"
	"github.com/openshift/ci-tools/pkg/api"
	"google.golang.org/protobuf/proto"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
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
	utilityConfig := generateUtilityConfig()
	utilityConfig.ExtraRefs = append(utilityConfig.ExtraRefs, generateReleaseRef())
	image := fmt.Sprintf("%s:%s", "applyconfig", api.LatestReleaseName)
	args := generateArgs(clusterName)
	args = append(args, "--confirm=true")
	container := generateContainer(image, args, generateVolumeMounts())
	volume := generateSecretVolume(clusterName)
	podSpec := generatePodSpec(container, []v1.Volume{volume})
	name := "periodic-openshift-release-master-" + clusterName + "-apply"
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.Labels = map[string]string{
		OpenshiftRole: Infra,
	}
	return prowconfig.Periodic{
		JobBase:  jobBase,
		Interval: "12h",
	}
}

func generatePostsubmit(clusterName string) prowconfig.Postsubmit {
	utilityConfig := generateUtilityConfig()
	args := generateArgs(clusterName)
	args = append(args, "--confirm=true")
	container := generateContainer(LatestImage, args, generateVolumeMounts())
	volume := generateSecretVolume(clusterName)
	podSpec := generatePodSpec(container, []v1.Volume{volume})
	name := "branch-ci-openshift-release-master-" + clusterName + "-apply"
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.MaxConcurrency = 1
	jobBase.Labels = map[string]string{
		OpenshiftRole: Infra,
	}
	brancher := prowconfig.Brancher{
		Branches: []string{"master"},
	}
	return prowconfig.Postsubmit{
		JobBase:  jobBase,
		Brancher: brancher,
	}
}

func generatePresubmit(clusterName string) prowconfig.Presubmit {
	utilityConfig := generateUtilityConfig()
	mounts := generateVolumeMounts()
	mounts = append(mounts, v1.VolumeMount{
		Name:      Tmp,
		MountPath: "/" + Tmp,
	})
	container := generateContainer(LatestImage, generateArgs(clusterName), mounts)
	secretVolume := generateSecretVolume(clusterName)
	emptyVolume := generateEmptyVolume(Tmp)
	podSpec := generatePodSpec(container, []v1.Volume{secretVolume, emptyVolume})
	name := "pull-ci-openshift-release-master-" + clusterName + "-dry"
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.Labels = map[string]string{
		"pj-rehearse.openshift.io/can-be-rehearsed": "true",
	}
	brancher := prowconfig.Brancher{
		Branches: []string{"master"},
	}
	rerun := prowconfig.DefaultRerunCommandFor(clusterName) + "-dry"
	trigger := prowconfig.DefaultTriggerFor(clusterName)
	reporter := generateReporter(clusterName)
	return prowconfig.Presubmit{
		JobBase:      jobBase,
		AlwaysRun:    true,
		Optional:     false,
		Trigger:      trigger,
		RerunCommand: rerun,
		Brancher:     brancher,
		Reporter:     reporter,
	}
}

func generateReporter(clusterName string) prowconfig.Reporter {
	return prowconfig.Reporter{
		Context: fmt.Sprintf("ci/build-farm/%s-dry", clusterName),
	}
}

func generateJobBase(ps *v1.PodSpec, uc prowconfig.UtilityConfig, name string) prowconfig.JobBase {
	return prowconfig.JobBase{
		Name:            name,
		Agent:           "kubernetes",
		Cluster:         string(api.ClusterAPPCI),
		Namespace:       nil,
		ErrorOnEviction: false,
		SourcePath:      "",
		Spec:            ps,
		PipelineRunSpec: nil,
		Annotations:     nil,
		ReporterConfig:  nil,
		RerunAuthConfig: nil,
		Hidden:          false,
		UtilityConfig:   uc,
	}
}

func generatePodSpec(c v1.Container, v []v1.Volume) *v1.PodSpec {
	return &v1.PodSpec{
		Volumes:            v,
		Containers:         []v1.Container{c},
		ServiceAccountName: ConfigUpdater,
	}
}

func generateReleaseRef() prowapi.Refs {
	return prowapi.Refs{
		Org:     "openshift",
		Repo:    "release",
		BaseRef: "master",
	}
}

func generateUtilityConfig() prowconfig.UtilityConfig {
	return prowconfig.UtilityConfig{
		Decorate:         proto.Bool(true),
		PathAlias:        "",
		CloneURI:         "",
		SkipSubmodules:   false,
		CloneDepth:       0,
		SkipFetchHead:    false,
		ExtraRefs:        []prowapi.Refs{},
		DecorationConfig: nil,
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
						Key:  secretConfigFor(ConfigUpdater, clusterName),
						Path: "kubeconfig",
					},
				},
			},
		},
	}
}

func generateEmptyVolume(name string) v1.Volume {
	return v1.Volume{
		Name: name,
		VolumeSource: v1.VolumeSource{
			EmptyDir: &v1.EmptyDirVolumeSource{},
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
