package prowgen

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	oauthTokenPath = "/usr/local/github-credentials"
	oauthKey       = "oauth"
)

type ProwgenInfo struct {
	cioperatorapi.Metadata
	Config config.Prowgen
}

// Generate a PodSpec that runs `ci-operator`, to be used in Presubmit/Postsubmit
// Various pieces are derived from `org`, `repo`, `branch` and `target`.
// `additionalArgs` are passed as additional arguments to `ci-operator`
func generatePodSpec(info *ProwgenInfo, secrets []*cioperatorapi.Secret, skipCloning bool) *corev1.PodSpec {
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "pull-secret",
			MountPath: "/etc/pull-secret",
			ReadOnly:  true,
		},
		{
			Name:      "result-aggregator",
			MountPath: "/etc/report",
			ReadOnly:  true,
		},
		{
			Name:      "gcs-credentials",
			MountPath: cioperatorapi.GCSUploadCredentialsSecretMountPath,
			ReadOnly:  true,
		},
	}

	volumes := []corev1.Volume{
		{
			Name: "pull-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "registry-pull-credentials"},
			},
		},
		{
			Name: "result-aggregator",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
			},
		},
	}

	for _, secret := range secrets {
		name := strings.ReplaceAll(secret.Name, ".", "-")
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      name,
			MountPath: fmt.Sprintf("/secrets/%s", secret.Name),
			ReadOnly:  true,
		})

		volumes = append(volumes, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: secret.Name},
			},
		})
	}

	if info.Config.Private {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      api.OauthTokenSecretName,
			MountPath: oauthTokenPath,
			ReadOnly:  true,
		})
		if skipCloning {
			volumes = append(volumes, corev1.Volume{
				Name: api.OauthTokenSecretName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: api.OauthTokenSecretName},
				},
			})
		}
	}

	return &corev1.PodSpec{
		ServiceAccountName: "ci-operator",
		Containers: []corev1.Container{
			{
				Image:           "ci-operator:latest",
				ImagePullPolicy: corev1.PullAlways,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
				},
				VolumeMounts: volumeMounts,
			},
		},
		Volumes: volumes,
	}
}

// GenerateJobs
// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additional
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
//
// All these generated jobs will be labeled as "newly generated". After all
// new jobs are generated with GenerateJobs, the callsite should also use
// Prune() function to remove all stale jobs and label the jobs as simply
// "generated".
func GenerateJobs(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) *prowconfig.JobConfig {
	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}
	var periodics []prowconfig.Periodic
	var jobRelease string
	if release, found := configSpec.Releases[cioperatorapi.LatestReleaseName]; found && release.Candidate != nil {
		jobRelease = release.Candidate.Version
	}

	skipCloning := true
	if configSpec.BuildRootImage != nil && configSpec.BuildRootImage.FromRepository {
		skipCloning = false
	}
	for _, element := range configSpec.Tests {
		var podSpec *corev1.PodSpec
		if element.Secret != nil {
			element.Secrets = append(element.Secrets, element.Secret)
		}
		if element.ContainerTestConfiguration != nil {
			podSpec = generateCiOperatorPodSpec(info, element.Secrets, []string{element.As}, skipCloning)
		} else if element.MultiStageTestConfiguration != nil || element.MultiStageTestConfigurationLiteral != nil {
			podSpec = generatePodSpecMultiStage(info, &element, configSpec.Releases != nil || element.ClusterClaim != nil, skipCloning)
		} else {
			var release string
			if c := configSpec.ReleaseTagConfiguration; c != nil {
				release = c.Name
			}
			podSpec = generatePodSpecTemplate(info, release, &element, skipCloning)
		}

		if element.Cron != nil || element.Interval != nil || element.ReleaseController {
			cron := ""
			if element.Cron != nil {
				cron = *element.Cron
			}
			interval := ""
			if element.Interval != nil {
				interval = *element.Interval
			}
			periodic := generatePeriodicForTest(element.As, info, podSpec, true, cron, interval, element.ReleaseController, configSpec.CanonicalGoRepository, jobRelease, skipCloning)
			if element.Cluster != "" {
				periodic.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			periodics = append(periodics, *periodic)
		} else if element.Postsubmit {
			postsubmit := generatePostsubmitForTest(element.As, info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning)
			postsubmit.MaxConcurrency = 1
			if element.Cluster != "" {
				postsubmit.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		} else {
			presubmit := *generatePresubmitForTest(element.As, info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, element.RunIfChanged, element.SkipIfOnlyChanged, element.Optional)
			v, requestingKVM := configSpec.Resources.RequirementsForStep(element.As).Requests[cioperatorapi.KVMDeviceLabel]
			if requestingKVM {
				presubmit.Labels[cioperatorapi.KVMDeviceLabel] = v
			}
			if element.Cluster != "" {
				presubmit.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			presubmits[orgrepo] = append(presubmits[orgrepo], presubmit)
		}
	}

	imageTargets := sets.NewString()
	if configSpec.PromotionConfiguration != nil {
		for additional := range configSpec.PromotionConfiguration.AdditionalImages {
			imageTargets.Insert(configSpec.PromotionConfiguration.AdditionalImages[additional])
		}
	}

	if len(configSpec.Images) > 0 || imageTargets.Len() > 0 {
		imageTargets.Insert("[images]")
	}

	if len(imageTargets) > 0 {
		// Identify which jobs need a to have a release payload explicitly requested
		var presubmitTargets = imageTargets.List()
		if promotion.PromotesOfficialImages(configSpec) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		podSpec := generateCiOperatorPodSpec(info, nil, presubmitTargets, skipCloning)
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest("images", info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false))

		if configSpec.PromotionConfiguration != nil {

			podSpec := generateCiOperatorPodSpec(info, nil, imageTargets.List(), skipCloning, []string{"--promote"}...)
			podSpec.Containers[0].Args = append(podSpec.Containers[0].Args,
				fmt.Sprintf("--image-mirror-push-secret=%s", filepath.Join(cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath, corev1.DockerConfigJsonKey)))
			podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "push-secret",
				MountPath: cioperatorapi.RegistryPushCredentialsCICentralSecretMountPath,
				ReadOnly:  true,
			})
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
				Name: "push-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: cioperatorapi.RegistryPushCredentialsCICentralSecret},
				},
			})
			postsubmit := generatePostsubmitForTest("images", info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning)
			postsubmit.MaxConcurrency = 1
			if postsubmit.Labels == nil {
				postsubmit.Labels = map[string]string{}
			}
			postsubmit.Labels[cioperatorapi.PromotionJobLabelKey] = "true"
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		}
	}

	if configSpec.Operator != nil {
		containsUnnamedBundle := false
		for _, bundle := range configSpec.Operator.Bundles {
			if bundle.As == "" {
				containsUnnamedBundle = true
				continue
			}
			indexName := api.IndexName(bundle.As)
			podSpec := generateCiOperatorPodSpec(info, nil, []string{indexName}, skipCloning)
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(indexName, info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false))
		}
		if containsUnnamedBundle {
			podSpec := generateCiOperatorPodSpec(info, nil, []string{string(api.PipelineImageStreamTagReferenceIndexImage)}, skipCloning)
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(string(api.PipelineImageStreamTagReferenceIndexImage), info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false))
		}
	}

	return &prowconfig.JobConfig{
		PresubmitsStatic:  presubmits,
		PostsubmitsStatic: postsubmits,
		Periodics:         periodics,
	}
}

func generateCiOperatorPodSpec(info *ProwgenInfo, secrets []*cioperatorapi.Secret, targets []string, skipCloning bool, additionalArgs ...string) *corev1.PodSpec {
	for _, arg := range additionalArgs {
		if !strings.HasPrefix(arg, "--") {
			panic(fmt.Sprintf("all args to ci-operator must be in the form --flag=value, not %s", arg))
		}
	}

	ret := generatePodSpec(info, secrets, skipCloning)
	ret.Containers[0].Command = []string{"ci-operator"}
	ret.Containers[0].Args = append([]string{
		"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
		"--gcs-upload-secret=/secrets/gcs/service-account.json",
		"--report-credentials-file=/etc/report/credentials",
	}, additionalArgs...)
	for _, target := range targets {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--target=%s", target))
	}
	if info.Config.Private {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--oauth-token-path=%s", filepath.Join(oauthTokenPath, oauthKey)))
	}
	for _, secret := range secrets {
		if secret.Name == api.HiveControlPlaneKubeconfigSecret {
			continue
		}
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--secret-dir=/secrets/%s", secret.Name))
	}

	if len(info.Variant) > 0 {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--variant=%s", info.Variant))
	}
	return ret
}

func generatePodSpecMultiStage(info *ProwgenInfo, test *cioperatorapi.TestStepConfiguration, needsPullSecret, skipCloning bool) *corev1.PodSpec {
	var profile api.ClusterProfile
	if test.MultiStageTestConfiguration != nil {
		profile = test.MultiStageTestConfiguration.ClusterProfile
	} else {
		profile = test.MultiStageTestConfigurationLiteral.ClusterProfile
	}
	var secrets []*cioperatorapi.Secret
	if needsPullSecret {
		// If the ci-operator configuration resolves an official release,
		// we need to create a pull secret in the namespace that ci-operator
		// runs in. While the --secret-dir mechanism is *meant* to provide
		// secrets to the tests themselves, this secret will have no consumer
		// and that is OK. We just need it to exist in the test namespace so
		// that the image import controller can use it.
		secrets = append(secrets, &cioperatorapi.Secret{
			Name: "ci-pull-credentials",
		})
	}
	var additionalArgs []string
	if test.ClusterClaim != nil {
		additionalArgs = []string{cioperatorapi.HiveControlPlaneKubeconfigSecretArg}
		secrets = append(secrets, &api.Secret{Name: api.HiveControlPlaneKubeconfigSecret})
	}
	podSpec := generateCiOperatorPodSpec(info, secrets, []string{test.As}, skipCloning, additionalArgs...)

	if profile != "" {
		podSpec.Volumes = append(podSpec.Volumes, generateClusterProfileVolume(profile, profile.ClusterType()))
		clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", test.As)
		container := &podSpec.Containers[0]
		container.Args = append(container.Args, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "cluster-profile", MountPath: clusterProfilePath})
	}

	if profile != "" || testContainsLease(test) {
		addLeaseClient(podSpec)
	}

	return podSpec
}

func testContainsLease(test *cioperatorapi.TestStepConfiguration) bool {
	// this is predicated upon the config being fully resolved at this time.
	if test.MultiStageTestConfigurationLiteral == nil {
		return false
	}

	return len(api.LeasesForTest(test.MultiStageTestConfigurationLiteral)) > 0
}

func generatePodSpecTemplate(info *ProwgenInfo, release string, test *cioperatorapi.TestStepConfiguration, skipCloning bool) *corev1.PodSpec {
	var testImageStreamTag, template string
	var clusterProfile cioperatorapi.ClusterProfile
	var needsReleaseRpms, needsLeaseServer bool
	if conf := test.OpenshiftAnsibleClusterTestConfiguration; conf != nil {
		template = "cluster-launch-e2e"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
	} else if conf := test.OpenshiftAnsibleSrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-src"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
	} else if conf := test.OpenshiftAnsibleCustomClusterTestConfiguration; conf != nil {
		template = "cluster-launch-e2e-openshift-ansible"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
	} else if conf := test.OpenshiftInstallerClusterTestConfiguration; conf != nil {
		if !conf.Upgrade {
			template = "cluster-launch-installer-e2e"
		}
		clusterProfile = conf.ClusterProfile
		needsLeaseServer = true
	} else if conf := test.OpenshiftInstallerUPIClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-upi-e2e"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerUPISrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-upi-src"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-custom-test-image"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
		testImageStreamTag = conf.From
	}
	clusterType := clusterProfile.ClusterType()
	clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", test.As)
	templatePath := fmt.Sprintf("/usr/local/%s", test.As)
	podSpec := generateCiOperatorPodSpec(info, test.Secrets, []string{test.As}, skipCloning)
	clusterProfileVolume := generateClusterProfileVolume(clusterProfile, clusterType)
	if len(template) > 0 {
		podSpec.Volumes = append(podSpec.Volumes, generateConfigMapVolume("job-definition", []string{fmt.Sprintf("prow-job-%s", template)}))
	}
	podSpec.Volumes = append(podSpec.Volumes, clusterProfileVolume)
	container := &podSpec.Containers[0]
	container.Args = append(container.Args, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
	if len(template) > 0 {
		container.Args = append(container.Args, fmt.Sprintf("--template=%s", templatePath))
	}
	if needsLeaseServer {
		addLeaseClient(podSpec)
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "cluster-profile", MountPath: clusterProfilePath})
	if len(template) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: "job-definition", MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", template)})
		container.Env = append(
			container.Env,
			corev1.EnvVar{Name: "CLUSTER_TYPE", Value: clusterType},
			corev1.EnvVar{Name: "JOB_NAME_SAFE", Value: strings.Replace(test.As, "_", "-", -1)},
			corev1.EnvVar{Name: "TEST_COMMAND", Value: test.Commands})
		if len(testImageStreamTag) > 0 {
			container.Env = append(container.Env,
				corev1.EnvVar{Name: "TEST_IMAGESTREAM_TAG", Value: testImageStreamTag})
		}
	}
	if needsReleaseRpms && (info.Org != "openshift" || info.Repo != "origin") {
		url := cioperatorapi.URLForService(cioperatorapi.ServiceRPMs)
		var repoPath = fmt.Sprintf("%s/openshift-origin-v%s/", url, release)
		if strings.HasPrefix(release, "origin-v") {
			repoPath = fmt.Sprintf("%s/openshift-%s/", url, release)
		}
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "RPM_REPO_OPENSHIFT_ORIGIN",
			Value: repoPath,
		})
	}

	return podSpec
}

func addLeaseClient(s *corev1.PodSpec) {
	s.Containers[0].Args = append(s.Containers[0].Args, "--lease-server-credentials-file=/etc/boskos/credentials")
	s.Containers[0].VolumeMounts = append(s.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "boskos",
		MountPath: "/etc/boskos",
		ReadOnly:  true,
	})
	s.Volumes = append(s.Volumes, corev1.Volume{
		Name: "boskos",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "boskos-credentials",
				Items:      []corev1.KeyToPath{{Key: "credentials", Path: "credentials"}},
			},
		},
	})
}

func generatePresubmitForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, pathAlias *string, jobRelease string, skipCloning bool, runIfChanged, skipIfOnlyChanged string, optional bool) *prowconfig.Presubmit {
	shortName := info.TestName(name)
	base := generateJobBase(name, jc.PresubmitPrefix, info, podSpec, true, pathAlias, jobRelease, skipCloning)
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: runIfChanged == "" && skipIfOnlyChanged == "",
		Brancher:  prowconfig.Brancher{Branches: sets.NewString(ExactlyBranch(info.Branch), FeatureBranch(info.Branch)).List()},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/prow/%s", shortName),
		},
		RerunCommand: prowconfig.DefaultRerunCommandFor(shortName),
		Trigger:      prowconfig.DefaultTriggerFor(shortName),
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      runIfChanged,
			SkipIfOnlyChanged: skipIfOnlyChanged,
		},
		Optional: optional,
	}
}

func generatePostsubmitForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, pathAlias *string, jobRelease string, skipCloning bool) *prowconfig.Postsubmit {
	base := generateJobBase(name, jc.PostsubmitPrefix, info, podSpec, false, pathAlias, jobRelease, skipCloning)
	return &prowconfig.Postsubmit{
		JobBase:  base,
		Brancher: prowconfig.Brancher{Branches: []string{ExactlyBranch(info.Branch)}},
	}
}

func generatePeriodicForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, rehearsable bool, cron string, interval string, releaseController bool, pathAlias *string, jobRelease string, skipCloning bool) *prowconfig.Periodic {
	base := generateJobBase(name, jc.PeriodicPrefix, info, podSpec, rehearsable, nil, jobRelease, skipCloning)
	// periodics are not associated with a repo per se, but we can add in an
	// extra ref so that periodics which want to access the repo tha they are
	// defined for can have that information
	ref := prowv1.Refs{
		Org:     info.Org,
		Repo:    info.Repo,
		BaseRef: info.Branch,
	}
	if pathAlias != nil {
		ref.PathAlias = *pathAlias
	}
	base.ExtraRefs = append([]prowv1.Refs{ref}, base.ExtraRefs...)
	if releaseController {
		interval = ""
		cron = "@yearly"
		base.Labels[jc.ReleaseControllerLabel] = jc.ReleaseControllerValue
	}
	return &prowconfig.Periodic{
		JobBase:  base,
		Cron:     cron,
		Interval: interval,
	}
}

func generateClusterProfileVolume(profile cioperatorapi.ClusterProfile, clusterType string) corev1.Volume {
	// AWS-2 and CPaaS and GCP2 PacketAssisted and PacketSNO need a different secret that should be provided to jobs
	if profile == cioperatorapi.ClusterProfileAWSCPaaS {
		clusterType = string(profile)
	} else if profile == cioperatorapi.ClusterProfileAWS2 {
		clusterType = string(profile)
	} else if profile == cioperatorapi.ClusterProfileGCP2 {
		clusterType = string(profile)
	} else if profile == cioperatorapi.ClusterProfilePacketAssisted {
		clusterType = string(profile)
	} else if profile == cioperatorapi.ClusterProfilePacketSNO {
		clusterType = string(profile)
	} else if profile == cioperatorapi.ClusterProfileAzure2 {
		clusterType = string(profile)
	}
	ret := corev1.Volume{
		Name: "cluster-profile",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{
					Secret: &corev1.SecretProjection{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: fmt.Sprintf("cluster-secrets-%s", clusterType),
						},
					}},
				},
			},
		},
	}
	switch profile {
	case
		cioperatorapi.ClusterProfileAWS,
		cioperatorapi.ClusterProfileAWSArm64,
		cioperatorapi.ClusterProfileAWSC2S,
		cioperatorapi.ClusterProfileAWSChina,
		cioperatorapi.ClusterProfileAWSGovCloud,
		cioperatorapi.ClusterProfileAlibaba,
		cioperatorapi.ClusterProfileAzure4,
		cioperatorapi.ClusterProfileAzure2,
		cioperatorapi.ClusterProfileAzureArc,
		cioperatorapi.ClusterProfileAzureStack,
		cioperatorapi.ClusterProfileIBMCloud,
		cioperatorapi.ClusterProfileLibvirtS390x,
		cioperatorapi.ClusterProfileLibvirtPpc64le,
		cioperatorapi.ClusterProfileOpenStack,
		cioperatorapi.ClusterProfileOpenStackKuryr,
		cioperatorapi.ClusterProfileOpenStackMechaCentral,
		cioperatorapi.ClusterProfileOpenStackMechaAz0,
		cioperatorapi.ClusterProfileOpenStackOsuosl,
		cioperatorapi.ClusterProfileOpenStackVexxhost,
		cioperatorapi.ClusterProfileOpenStackPpc64le,
		cioperatorapi.ClusterProfileVSphere,
		cioperatorapi.ClusterProfileKubevirt,
		cioperatorapi.ClusterProfileAWSCPaaS,
		cioperatorapi.ClusterProfileOSDEphemeral,
		cioperatorapi.ClusterProfileAWS2,
		cioperatorapi.ClusterProfileHyperShift,
		cioperatorapi.ClusterProfilePacket,
		cioperatorapi.ClusterProfilePacketAssisted,
		cioperatorapi.ClusterProfilePacketSNO:
	default:
		ret.VolumeSource.Projected.Sources = append(ret.VolumeSource.Projected.Sources, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("cluster-profile-%s", profile),
				},
			},
		})
	}
	return ret
}

func generateConfigMapVolume(name string, templates []string) corev1.Volume {
	ret := corev1.Volume{Name: name}
	switch len(templates) {
	case 0:
	case 1:
		ret.VolumeSource = corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: templates[0],
				},
			},
		}
	default:
		ret.VolumeSource = corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{},
		}
		s := &ret.VolumeSource.Projected.Sources
		for _, t := range templates {
			*s = append(*s, corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: t,
					},
				},
			})
		}
	}
	return ret
}

func generateJobBase(name, prefix string, info *ProwgenInfo, podSpec *corev1.PodSpec, rehearsable bool, pathAlias *string, jobRelease string, skipCloning bool) prowconfig.JobBase {
	labels := map[string]string{jc.LabelGenerated: string(jc.NewlyGenerated)}

	if rehearsable {
		labels[jc.CanBeRehearsedLabel] = jc.CanBeRehearsedValue
	}

	jobName := info.JobName(prefix, name)
	if len(info.Variant) > 0 {
		labels[jc.ProwJobLabelVariant] = info.Variant
	}
	if jobRelease != "" {
		labels[jc.JobReleaseKey] = jobRelease
	}

	var decorationConfig *prowv1.DecorationConfig
	if skipCloning {
		decorationConfig = &prowv1.DecorationConfig{SkipCloning: utilpointer.BoolPtr(true)}
	} else if !skipCloning && info.Config.Private {
		decorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: api.OauthTokenSecretKey, Name: api.OauthTokenSecretName}}
	}
	base := prowconfig.JobBase{
		Agent:  string(prowv1.KubernetesAgent),
		Labels: labels,
		Name:   jobName,
		Spec:   podSpec,
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: decorationConfig,
			Decorate:         utilpointer.BoolPtr(true),
		},
	}
	if pathAlias != nil {
		base.PathAlias = *pathAlias
	}
	if info.Config.Private && !info.Config.Expose {
		base.Hidden = true
	}
	return base
}

// ExactlyBranch returns a regex string that matches exactly the given branch name: I.e. returns
// '^master$' for 'master'. If the given branch name already looks like a regex, return it unchanged.
func ExactlyBranch(branch string) string {
	if !jc.SimpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s$", regexp.QuoteMeta(branch))
}

// FeatureBranch returns a regex string that matches feature branch prefixes for the given branch name:
// I.e. returns '^master-' for 'master'. If the given branch name already looks like a regex,
// return it unchanged.
func FeatureBranch(branch string) string {
	if !jc.SimpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s-", regexp.QuoteMeta(branch))
}
