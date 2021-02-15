package multi_stage

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/entrypoint"

	"github.com/openshift/ci-tools/pkg/api"
	base_steps "github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

const (
	containerName     = "test"
	profileVolumeName = "cluster-profile"
	vpnContainerName  = "vpn-client"
)

func (s *multiStageTestStep) generateObservers(
	observers []api.Observer,
	secretVolumes []coreapi.Volume,
	secretVolumeMounts []coreapi.VolumeMount,
) ([]coreapi.Pod, error) {
	var adapted []api.LiteralTestStep
	for _, observer := range observers {
		// observers are just like steps, so we can adapt one to the other
		adapted = append(adapted, api.LiteralTestStep{
			As:          observer.Name,
			From:        observer.From,
			FromImage:   observer.FromImage,
			Commands:    observer.Commands,
			Resources:   observer.Resources,
		})
	}
	pods, _, err := s.generatePods(adapted, nil, secretVolumes, secretVolumeMounts)
	return pods, err
}

func (s *multiStageTestStep) generatePods(
	steps []api.LiteralTestStep,
	env []coreapi.EnvVar,
	secretVolumes []coreapi.Volume,
	secretVolumeMounts []coreapi.VolumeMount,
) ([]coreapi.Pod, sets.String, error) {
	var bestEffortSteps sets.String
	if s.flags&allowBestEffortPostSteps != 0 {
		bestEffortSteps = sets.NewString()
	}
	var ret []coreapi.Pod
	var errs []error
	var claimRelease *api.ClaimRelease
	if s.clusterClaim != nil {
		claimRelease = s.clusterClaim.ClaimRelease(s.name)
	}
	for _, step := range steps {
		name := fmt.Sprintf("%s-%s", s.name, step.As)
		if o := step.OptionalOnSuccess; o != nil && *o && s.flags&allowSkipOnSuccess != 0 && s.flags&hasPrevErrs == 0 {
			logrus.Infof(fmt.Sprintf("Skipping optional step %s", name))
			continue
		}
		image := step.From
		if link, ok := step.FromImageTag(); ok {
			image = fmt.Sprintf("%s:%s", api.PipelineImageStream, link)
		} else {
			dep := api.StepDependency{Name: image}
			stream, tag, _ := s.config.DependencyParts(dep, claimRelease)
			image = fmt.Sprintf("%s:%s", stream, tag)
		}
		resources, err := base_steps.ResourcesFor(step.Resources)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allResources := &resources
		if !resources.Requests.Name(api.ShmResource, resource.BinarySI).IsZero() {
			// If shm is in Limits it must also be in Requests
			allResources = resources.DeepCopy()
			logrus.Info("removing shm from resources for container")
			delete(resources.Requests, api.ShmResource)
			delete(resources.Limits, api.ShmResource)
		}
		if bestEffortSteps != nil && step.BestEffort != nil && *step.BestEffort {
			bestEffortSteps.Insert(name)
		}
		p := func(i int64) *int64 {
			return &i
		}
		artifactDir := fmt.Sprintf("%s/%s", s.name, step.As)
		timeout := entrypoint.DefaultTimeout
		if step.Timeout != nil {
			timeout = step.Timeout.Duration
		}
		s.jobSpec.DecorationConfig.Timeout = &prowapi.Duration{Duration: timeout}
		gracePeriod := entrypoint.DefaultGracePeriod
		if step.GracePeriod != nil {
			gracePeriod = step.GracePeriod.Duration
		}
		s.jobSpec.DecorationConfig.GracePeriod = &prowapi.Duration{Duration: gracePeriod}
		// We want upload to have some time to do what it needs to do, so set
		// the grace period for the Pod to be just larger than the grace period
		// for the process, assuming an 80/20 distribution of work.
		terminationGracePeriodSeconds := p(int64(gracePeriod.Seconds() * 5 / 4))
		var commands []string
		if step.RunAsScript != nil && *step.RunAsScript {
			commands = []string{fmt.Sprintf("%s/%s", CommandScriptMountPath, step.As)}
		} else {
			commands = []string{"/bin/bash", "-c", CommandPrefix + step.Commands}
		}
		labels := map[string]string{base_steps.LabelMetadataStep: step.As}
		pod, err := base_steps.GenerateBasePod(s.jobSpec, labels, name, s.nodeName, containerName, commands, image, resources, artifactDir, s.jobSpec.DecorationConfig, s.jobSpec.RawSpec(), secretVolumeMounts, false)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		delete(pod.Labels, base_steps.ProwJobIdLabel)
		pod.Annotations[base_steps.AnnotationSaveContainerLogs] = "true"
		pod.Labels[MultiStageTestLabel] = s.name
		pod.Spec.ServiceAccountName = s.name
		pod.Spec.TerminationGracePeriodSeconds = terminationGracePeriodSeconds
		if step.DNSConfig != nil {
			if pod.Spec.DNSConfig == nil {
				pod.Spec.DNSConfig = &coreapi.PodDNSConfig{}
			}
			pod.Spec.DNSConfig.Nameservers = append(pod.Spec.DNSConfig.Nameservers, step.DNSConfig.Nameservers...)
			pod.Spec.DNSConfig.Searches = append(pod.Spec.DNSConfig.Searches, step.DNSConfig.Searches...)
			if len(pod.Spec.DNSConfig.Nameservers) > 0 {
				pod.Spec.DNSPolicy = coreapi.DNSNone
			}
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{Name: homeVolumeName, VolumeSource: coreapi.VolumeSource{EmptyDir: &coreapi.EmptyDirVolumeSource{}}})
		pod.Spec.Volumes = append(pod.Spec.Volumes, secretVolumes...)
		for idx := range pod.Spec.Containers {
			if pod.Spec.Containers[idx].Name != containerName {
				continue
			}
			pod.Spec.Containers[idx].VolumeMounts = append(pod.Spec.Containers[idx].VolumeMounts, coreapi.VolumeMount{Name: homeVolumeName, MountPath: "/alabama"})
		}

		addSecretWrapper(pod, s.vpnConf)
		if s.vpnConf != nil {
			s.addVPNClient(pod)
		}
		container := &pod.Spec.Containers[0]
		container.Env = append(container.Env, []coreapi.EnvVar{
			{Name: "NAMESPACE", Value: s.jobSpec.Namespace()},
			{Name: "JOB_NAME_SAFE", Value: strings.Replace(s.name, "_", "-", -1)},
			{Name: "JOB_NAME_HASH", Value: s.jobSpec.JobNameHash()},
		}...)
		container.Env = append(container.Env, env...)
		container.Env = append(container.Env, s.generateParams(step.Environment)...)
		depEnv, depErrs := s.envForDependencies(step)
		if len(depErrs) != 0 {
			errs = append(errs, depErrs...)
			continue
		}
		container.Env = append(container.Env, depEnv...)
		if owner := s.jobSpec.Owner(); owner != nil {
			pod.OwnerReferences = append(pod.OwnerReferences, *owner)
		}
		if s.profile != "" && s.clusterClaim != nil {
			// should never happen
			errs = append(errs, fmt.Errorf("cannot set both cluster_profile and cluster_claim in a test"))
		}
		if s.clusterClaim != nil {
			clusterClaimEnv, clusterClaimMount, err := getClusterClaimPodParams(secretVolumeMounts, s.name)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get cluster claim pod params: %w", err))
			} else {
				container.Env = append(container.Env, clusterClaimEnv...)
				// The volumes are there already because sidecar container uses them.
				// We mount them here to the test container.
				container.VolumeMounts = append(container.VolumeMounts, clusterClaimMount...)
			}
		} else {
			container.Env = append(container.Env, []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: filepath.Join(SecretMountPath, "kubeconfig")},
				{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(SecretMountPath, "kubeadmin-password")},
			}...)
		}
		shmSize := allResources.Requests.Name(api.ShmResource, resource.BinarySI)
		if !shmSize.IsZero() {
			addDshmVolume(shmSize, pod, container)
		}
		if s.profile != "" {
			addProfile(s.profileSecretName(), s.profile, pod)
		}
		if step.Cli != "" {
			dependency := api.StepDependency{Name: fmt.Sprintf("%s:cli", api.ReleaseStreamFor(step.Cli))}
			imagestream, _, _ := s.config.DependencyParts(dependency, claimRelease)
			addCliInjector(imagestream, pod)
		}
		addSharedDirSecret(s.name, pod)
		addCredentials(step.Credentials, pod)
		if step.RunAsScript != nil && *step.RunAsScript {
			addCommandScript(commandConfigMapForTest(s.name), pod)
		}
		if s.vpnConf != nil {
			caps := coreapi.Capabilities{
				Add:  []coreapi.Capability{"NET_ADMIN"},
				Drop: []coreapi.Capability{"ALL"},
			}
			seLinuxOpts := coreapi.SELinuxOptions{
				User: "system_u",
				Role: "system_r",
				// TODO create a more restricted SELinux context
				// This one happens to be in every cluster and have the
				// permission to use /dev/net/tun and configure networking, but
				// has *many* more permissions than are required here.
				Type:  "container_runtime_t",
				Level: "s0",
			}
			setSecurityContexts(pod, vpnContainerName, s.vpnConf.namespaceUID, &caps, &seLinuxOpts)
		}
		ret = append(ret, *pod)
	}
	return ret, bestEffortSteps, utilerrors.NewAggregate(errs)
}

func addSecretWrapper(pod *coreapi.Pod, vpnConf *vpnConf) {
	volume := "entrypoint-wrapper"
	dir := "/tmp/entrypoint-wrapper"
	bin := filepath.Join(dir, "entrypoint-wrapper")
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volume,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	mount := coreapi.VolumeMount{Name: volume, MountPath: dir}
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Image:                    fmt.Sprintf("%s/ci/entrypoint-wrapper:latest", api.DomainForService(api.ServiceRegistry)),
		Name:                     "cp-entrypoint-wrapper",
		Command:                  []string{"cp"},
		Args:                     []string{"/bin/entrypoint-wrapper", bin},
		VolumeMounts:             []coreapi.VolumeMount{mount},
		TerminationMessagePolicy: coreapi.TerminationMessageFallbackToLogsOnError,
	})
	container := &pod.Spec.Containers[0]
	args := container.Args
	container.Args = make([]string, 0)
	if c := vpnConf; c != nil && c.WaitTimeout != nil {
		container.Args = append(container.Args,
			"--wait-for-file", "/tmp/vpn/up",
			"--wait-timeout", *c.WaitTimeout)
	}
	container.Args = append(container.Args, container.Command...)
	container.Args = append(container.Args, args...)
	container.Command = []string{bin}
	container.VolumeMounts = append(container.VolumeMounts, mount)
}

func (s *multiStageTestStep) addVPNClient(pod *coreapi.Pod) {
	profileMount := "/tmp/profile"
	vpnVolMount := coreapi.VolumeMount{Name: "vpn", MountPath: "/tmp/vpn"}
	container := coreapi.Container{
		Name:       vpnContainerName,
		Image:      s.vpnConf.Image,
		Command:    []string{"bash", "-c", s.vpnConf.Commands},
		WorkingDir: profileMount,
		VolumeMounts: []coreapi.VolumeMount{
			{Name: "tun", MountPath: "/dev/net/tun"},
			vpnVolMount,
			{Name: "logs", MountPath: "/logs"},
			{Name: profileVolumeName, MountPath: profileMount},
		},
	}
	pod.Spec.Containers = append(pod.Spec.Containers, container)
	charDev := coreapi.HostPathCharDev
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: "vpn",
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: "tun",
		VolumeSource: coreapi.VolumeSource{
			HostPath: &coreapi.HostPathVolumeSource{
				Path: "/dev/net/tun",
				Type: &charDev,
			},
		},
	})
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, vpnVolMount)
}

// setSecurityContexts configures the context of all containers in a pod
// `root` specifies a container (or init container) which should be run as UID 0
// and with `capabilities` and `seLinuxOpts`.  All others are explicitly set to
// run as non-root with `uid`.  The latter is necessary since the SCC defaults
// apply to all containers.
func setSecurityContexts(
	pod *coreapi.Pod,
	root string,
	uid int64,
	capabilities *coreapi.Capabilities,
	seLinuxOpts *coreapi.SELinuxOptions,
) {
	f := func(l []coreapi.Container) {
		for i := range l {
			if l[i].Name == root {
				var uid int64
				l[i].SecurityContext = &coreapi.SecurityContext{
					RunAsUser:      &uid,
					Capabilities:   capabilities,
					SELinuxOptions: seLinuxOpts,
				}
			} else {
				nonRoot := true
				l[i].SecurityContext = &coreapi.SecurityContext{
					RunAsNonRoot: &nonRoot,
					RunAsUser:    &uid,
				}
			}
		}
	}
	f(pod.Spec.InitContainers)
	f(pod.Spec.Containers)
}

func (s *multiStageTestStep) generateParams(env []api.StepParameter) []coreapi.EnvVar {
	var ret []coreapi.EnvVar
	for _, env := range env {
		value := ""
		if env.Default != nil {
			value = *env.Default
		}
		if v, ok := s.env[env.Name]; ok {
			value = v
		}
		ret = append(ret, coreapi.EnvVar{Name: env.Name, Value: value})
	}
	return ret
}

func (s *multiStageTestStep) envForDependencies(step api.LiteralTestStep) ([]coreapi.EnvVar, []error) {
	var env []coreapi.EnvVar
	var errs []error
	var claimRelease *api.ClaimRelease
	if s.clusterClaim != nil {
		claimRelease = s.clusterClaim.ClaimRelease(s.name)
	}
	for _, dependency := range step.Dependencies {
		var ref string
		// if a fully-qualified pull spec was provided, then just use that. It'll be up to the job to use that pull spec
		// correctly as it could possibly point to an external registry that ci-operator will itself not have access to.
		if dependency.PullSpec != "" {
			ref = dependency.PullSpec
		} else {
			imageStream, name, _ := s.config.DependencyParts(dependency, claimRelease)
			depRef, err := utils.ImageDigestFor(s.client, s.jobSpec.Namespace, imageStream, name)()
			if err != nil {
				errs = append(errs, fmt.Errorf("could not determine image pull spec for image %s on step %s", dependency.Name, step.As))
				continue
			}
			ref = depRef
		}
		env = append(env, coreapi.EnvVar{
			Name: dependency.Env, Value: ref,
		})
	}
	return env, errs
}

func getClusterClaimPodParams(secretVolumeMounts []coreapi.VolumeMount, testName string) ([]coreapi.EnvVar, []coreapi.VolumeMount, error) {
	var retEnv []coreapi.EnvVar
	var retMount []coreapi.VolumeMount
	var errs []error

	for _, secretName := range []string{api.HiveAdminKubeconfigSecret, api.HiveAdminPasswordSecret} {
		mountPath := getMountPath(base_steps.NamePerTest(secretName, testName))
		var foundMountPath bool
		for _, secretVolumeMount := range secretVolumeMounts {
			if secretVolumeMount.MountPath == mountPath {
				foundMountPath = true
				retMount = append(retMount, secretVolumeMount)
				if secretName == api.HiveAdminKubeconfigSecret {
					retEnv = append(retEnv, coreapi.EnvVar{Name: "KUBECONFIG", Value: filepath.Join(secretVolumeMount.MountPath, api.HiveAdminKubeconfigSecretKey)})
				}
				if secretName == api.HiveAdminPasswordSecret {
					retEnv = append(retEnv, coreapi.EnvVar{Name: "KUBEADMIN_PASSWORD_FILE", Value: filepath.Join(secretVolumeMount.MountPath, api.HiveAdminPasswordSecretKey)})
				}
				break
			}
		}
		if !foundMountPath {
			// should never happen
			errs = append(errs, fmt.Errorf("failed to find foundMountPath %s to create secret %s", mountPath, base_steps.NamePerTest(secretName, testName)))
		}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}
	return retEnv, retMount, nil
}

func addDshmVolume(shmSize *resource.Quantity, pod *coreapi.Pod, container *coreapi.Container) {
	logrus.Infof("Adding Dshm Volume to pod: %s", pod.Name)
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: "dshm",
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{
				Medium:    coreapi.StorageMediumMemory,
				SizeLimit: shmSize}},
	})
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      "dshm",
		MountPath: "/dev/shm",
	})
}

func addProfile(name string, profile api.ClusterProfile, pod *coreapi.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: profileVolumeName,
		VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{
				SecretName: name,
			},
		},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      profileVolumeName,
		MountPath: ClusterProfileMountPath,
	})
	container.Env = append(container.Env, []coreapi.EnvVar{{
		Name:  "CLUSTER_TYPE",
		Value: profile.ClusterType(),
	}, {
		Name:  ClusterProfileMountEnv,
		Value: ClusterProfileMountPath,
	}}...)
}

func addCliInjector(imagestream string, pod *coreapi.Pod) {
	volumeName := "cli"
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			EmptyDir: &coreapi.EmptyDirVolumeSource{},
		},
	})
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, coreapi.Container{
		Name:    "inject-cli",
		Image:   fmt.Sprintf("%s:cli", imagestream),
		Command: []string{"/bin/cp"},
		Args:    []string{"/usr/bin/oc", CliMountPath},
		VolumeMounts: []coreapi.VolumeMount{{
			Name:      volumeName,
			MountPath: CliMountPath,
		}},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      volumeName,
		MountPath: CliMountPath,
	})
	container.Env = append(container.Env, coreapi.EnvVar{
		Name:  api.CliEnv,
		Value: CliMountPath,
	})
}

func addSharedDirSecret(secret string, pod *coreapi.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: secret,
		VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{SecretName: secret},
		},
	})
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
		Name:      secret,
		MountPath: SecretMountPath,
	})
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, coreapi.EnvVar{
		Name:  SecretMountEnv,
		Value: SecretMountPath,
	})
}

func addCredentials(credentials []api.CredentialReference, pod *coreapi.Pod) {
	for _, credential := range credentials {
		name := fmt.Sprintf("%s-%s", credential.Namespace, credential.Name)
		volumeName := volumeName(credential.Namespace, credential.Name)
		pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
			Name: volumeName,
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{SecretName: name},
			},
		})
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, coreapi.VolumeMount{
			Name:      volumeName,
			MountPath: credential.MountPath,
		})
	}
}

func commandConfigMapForTest(testName string) string {
	return fmt.Sprintf("%s-commands", testName)
}

func addCommandScript(name string, pod *coreapi.Pod) {
	volumeName := "commands-script"
	// 0777 in decimal is 511
	mode := int32(511)
	pod.Spec.Volumes = append(pod.Spec.Volumes, coreapi.Volume{
		Name: volumeName,
		VolumeSource: coreapi.VolumeSource{
			ConfigMap: &coreapi.ConfigMapVolumeSource{
				LocalObjectReference: coreapi.LocalObjectReference{
					Name: name,
				},
				DefaultMode: &mode,
			},
		},
	})
	container := &pod.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts, coreapi.VolumeMount{
		Name:      volumeName,
		MountPath: CommandScriptMountPath,
	})
}
