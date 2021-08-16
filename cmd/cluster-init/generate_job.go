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
	Openshift     = "openshift"
	Release       = "release"
	Master        = "master"
	ApplyConfig   = "applyconfig"
	Latest        = "latest"
	ConfigDir     = "config-dir"
	Clusters      = "clusters"
	BuildClusters = "build-clusters"
	Confirm       = "confirm"
	As            = "as"
	Always        = "Always"
	Cpu           = "cpu"
	ConfigUpdater = "config-updater"
	Kubernetes    = "Kubernetes"
	AppDotCi      = "app.ci"
	Periodic      = "periodic"
	Apply         = "apply"
	Ci            = "ci"
	Io            = "io"
	Role          = "role"
	Infra         = "infra"
	Registry      = "registry"
	Org           = "org"
	Branch        = "branch"
)

func GeneratePeriodic(clusterName string, buildFarmDir string) prowconfig.Periodic {
	utilityConfig := generateUtilityConfig()
	utilityConfig.ExtraRefs = append(utilityConfig.ExtraRefs, generateReleaseRef())
	image := fmt.Sprintf("%s:%s", ApplyConfig, Latest)
	container := generateContainer(buildFarmDir, image)
	volume := generateVolume(clusterName)
	podSpec := generatePodSpec(volume, container)
	name := fmt.Sprintf("%s-%s-%s-%s-%s-%s", Periodic, Openshift, Release, Master, clusterName, Apply)
	jobBase := generateJobBase(clusterName, podSpec, utilityConfig, name, 0)
	return prowconfig.Periodic{
		JobBase:  jobBase,
		Interval: "12h",
	}
}

func GeneratePostsubmit(clusterName string, buildFarmDir string) prowconfig.Postsubmit {
	utilityConfig := generateUtilityConfig()
	image := fmt.Sprintf("%s.%s.%s.%s/%s/%s:%s", Registry, Ci, Openshift, Org, Ci, ApplyConfig, Latest)
	container := generateContainer(buildFarmDir, image)
	volume := generateVolume(clusterName)
	podSpec := generatePodSpec(volume, container)
	name := fmt.Sprintf("%s-%s-%s-%s-%s-%s-%s", Branch, Ci, Openshift, Release, Master, clusterName, Apply)
	jobBase := generateJobBase(clusterName, podSpec, utilityConfig, name, 1)
	brancher := prowconfig.Brancher{
		Branches: []string{"master"},
	}
	return prowconfig.Postsubmit{
		JobBase:  jobBase,
		Brancher: brancher,
	}
}

func generateJobBase(clusterName string, ps *v1.PodSpec, uc prowconfig.UtilityConfig, name string, maxConcurrency int) prowconfig.JobBase {
	return prowconfig.JobBase{
		Name: name,
		Labels: map[string]string{
			fmt.Sprintf("%s.%s.%s/%s", Ci, Openshift, Io, Role): Infra,
		},
		MaxConcurrency:  maxConcurrency,
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

func generatePodSpec(v v1.Volume, c v1.Container) *v1.PodSpec {
	return &v1.PodSpec{
		Volumes:            []v1.Volume{v},
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

func generateVolume(clusterName string) v1.Volume {
	return v1.Volume{
		Name: BuildFarmCredentials,
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: BuildFarmCredentials,
				Items: []v1.KeyToPath{
					{
						Key:  fmt.Sprintf("%s.%s.%s", SaConfigUpdater, clusterName, Config),
						Path: "kubeconfig",
					},
				},
			},
		},
	}
}

func generateContainer(buildFarmDir string, image string) v1.Container {
	return v1.Container{
		Name:    "",
		Image:   image,
		Command: []string{ApplyConfig},
		Args: []string{
			fmt.Sprintf(fmt.Sprintf("--%s=%s/%s/%s", ConfigDir, Clusters, BuildClusters, buildFarmDir)),
			fmt.Sprintf("--%s=true", Confirm),
			fmt.Sprintf("--%s=", As),
			fmt.Sprintf("--%s=/%s/%s/%s", Kubeconfig, Etc, BuildFarmCredentials, Kubeconfig),
		},
		Resources: v1.ResourceRequirements{
			Requests: map[v1.ResourceName]resource.Quantity{
				Cpu: resource.MustParse("10m"),
			},
		},
		ImagePullPolicy: Always,
		VolumeMounts: []v1.VolumeMount{
			{
				Name:      BuildFarmCredentials,
				ReadOnly:  true,
				MountPath: fmt.Sprintf("/%s/%s", Etc, BuildFarmCredentials),
			},
		},
	}
}
