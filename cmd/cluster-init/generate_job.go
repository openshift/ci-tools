package main

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
)

//TODO: not sure some of these should be constants, what if we only use them once???
const (
	Openshift      = "openshift"
	Release        = "release"
	Master         = "master"
	ApplyConfig    = "applyconfig"
	Latest         = "latest"
	ConfigDir      = "config-dir"
	Clusters       = "clusters"
	Confirm        = "confirm"
	As             = "as"
	Always         = "Always"
	Cpu            = "cpu"
	Kubernetes     = "kubernetes"
	AppDotCi       = "app.ci"
	Periodic       = "periodic"
	Apply          = "apply"
	Ci             = "ci"
	Io             = "io"
	Role           = "role"
	Infra          = "infra"
	Registry       = "registry"
	Org            = "org"
	Branch         = "branch"
	Dry            = "dry"
	BuildFarm      = "build-farm"
	Pull           = "pull"
	CanBeRehearsed = "can-be-rehearsed"
	Tmp            = "tmp"
)

func generatePeriodic(clusterName string, buildFarmDir string) prowconfig.Periodic {
	utilityConfig := generateUtilityConfig()
	utilityConfig.ExtraRefs = append(utilityConfig.ExtraRefs, generateReleaseRef())
	image := fmt.Sprintf("%s:%s", ApplyConfig, Latest)
	args := generateArgs(buildFarmDir)
	args = append(args, fmt.Sprintf("--%s=true", Confirm))
	container := generateContainer(image, args, generateVolumeMounts())
	volume := generateSecretVolume(clusterName)
	podSpec := generatePodSpec(container, []v1.Volume{volume})
	name := fmt.Sprintf("%s-%s-%s-%s-%s-%s", Periodic, Openshift, Release, Master, clusterName, Apply)
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.Labels = map[string]string{
		fmt.Sprintf("%s.%s.%s/%s", Ci, Openshift, Io, Role): Infra,
	}
	return prowconfig.Periodic{
		JobBase:  jobBase,
		Interval: "12h",
	}
}

func generatePostsubmit(clusterName string, buildFarmDir string) prowconfig.Postsubmit {
	utilityConfig := generateUtilityConfig()
	image := fmt.Sprintf("%s.%s.%s.%s/%s/%s:%s", Registry, Ci, Openshift, Org, Ci, ApplyConfig, Latest)
	args := generateArgs(buildFarmDir)
	args = append(args, fmt.Sprintf("--%s=true", Confirm))
	container := generateContainer(image, args, generateVolumeMounts())
	volume := generateSecretVolume(clusterName)
	podSpec := generatePodSpec(container, []v1.Volume{volume})
	name := fmt.Sprintf("%s-%s-%s-%s-%s-%s-%s", Branch, Ci, Openshift, Release, Master, clusterName, Apply)
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.MaxConcurrency = 1
	jobBase.Labels = map[string]string{
		fmt.Sprintf("%s.%s.%s/%s", Ci, Openshift, Io, Role): Infra,
	}
	brancher := prowconfig.Brancher{
		Branches: []string{"master"},
	}
	return prowconfig.Postsubmit{
		JobBase:  jobBase,
		Brancher: brancher,
	}
}

func generatePresubmit(clusterName string, buildFarmDir string) prowconfig.Presubmit {
	utilityConfig := generateUtilityConfig()
	image := fmt.Sprintf("%s.%s.%s.%s/%s/%s:%s", Registry, Ci, Openshift, Org, Ci, ApplyConfig, Latest)
	mounts := generateVolumeMounts()
	mounts = append(mounts, v1.VolumeMount{
		Name:      Tmp,
		MountPath: fmt.Sprintf("/%s", Tmp),
	})
	container := generateContainer(image, generateArgs(buildFarmDir), mounts)
	secretVolume := generateSecretVolume(clusterName)
	emptyVolume := generateEmptyVolume(Tmp)
	podSpec := generatePodSpec(container, []v1.Volume{secretVolume, emptyVolume})
	name := fmt.Sprintf("%s-%s-%s-%s-%s-%s-%s", Pull, Ci, Openshift, Release, Master, clusterName, Dry)
	jobBase := generateJobBase(podSpec, utilityConfig, name)
	jobBase.Labels = map[string]string{
		fmt.Sprintf("%s.%s.%s/%s", PjRehearse, Openshift, Io, CanBeRehearsed): "true",
	}
	brancher := prowconfig.Brancher{
		Branches: []string{"master"},
	}
	rerun := fmt.Sprintf("/%s %s-%s", Test, clusterName, Dry)
	trigger := fmt.Sprintf("(?m)^/%s( | .* )%s-%s,?($|\\s.*)", Test, clusterName, Dry)
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
		Context: fmt.Sprintf("%s/%s/%s-%s", Ci, BuildFarm, clusterName, Dry),
	}
}

func generateJobBase(ps *v1.PodSpec, uc prowconfig.UtilityConfig, name string) prowconfig.JobBase {
	return prowconfig.JobBase{
		Name:            name,
		Agent:           Kubernetes,
		Cluster:         AppDotCi,
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
		Org:     Openshift,
		Repo:    Release,
		BaseRef: Master,
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
		Name: BuildFarmCredentials,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: BuildFarmCredentials,
				Items: []v1.KeyToPath{
					{
						Key:  fmt.Sprintf("%s.%s.%s.%s", Sa, ConfigUpdater, clusterName, Config),
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
		Command: []string{ApplyConfig},
		Args:    args,
		Resources: v1.ResourceRequirements{
			Requests: map[v1.ResourceName]resource.Quantity{
				Cpu: resource.MustParse("10m"),
			},
		},
		ImagePullPolicy: Always,
		VolumeMounts:    volumeMounts,
	}
}

func generateArgs(buildFarmDir string) []string {
	return []string{
		fmt.Sprintf(fmt.Sprintf("--%s=%s/%s/%s", ConfigDir, Clusters, BuildClusters, buildFarmDir)),
		fmt.Sprintf("--%s=", As),
		fmt.Sprintf("--%s=/%s/%s/%s", Kubeconfig, Etc, BuildFarmCredentials, Kubeconfig),
	}
}

func generateVolumeMounts() []v1.VolumeMount {
	return []v1.VolumeMount{
		{
			Name:      BuildFarmCredentials,
			ReadOnly:  true,
			MountPath: fmt.Sprintf("/%s/%s", Etc, BuildFarmCredentials),
		},
	}
}
