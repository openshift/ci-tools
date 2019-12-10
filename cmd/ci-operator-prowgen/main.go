package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	kubeapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/migrate"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	prowJobLabelVariant = "ci-operator.openshift.io/variant"

	sentryDsnMountName  = "sentry-dsn"
	sentryDsnSecretName = "sentry-dsn"
	sentryDsnMountPath  = "/etc/sentry-dsn"
	sentryDsnSecretPath = "/etc/sentry-dsn/ci-operator"

	openshiftInstallerRandomCmd = `set -eux
target=$(awk < /usr/local/e2e-targets \
    --assign "r=$RANDOM" \
    'BEGIN { r /= 32767 } (r -= $1) <= 0 { print $2; exit }')
case "$target" in
    aws) template=e2e; CLUSTER_TYPE=aws;;
    azure) template=e2e; CLUSTER_TYPE=azure4;;
    aws-upi) template=upi-e2e; CLUSTER_TYPE=aws;;
    vsphere) template=upi-e2e; CLUSTER_TYPE=vsphere;;
    *) echo >&2 "invalid target $target"; exit 1 ;;
esac
ln -s "/usr/local/job-definition/cluster-launch-installer-$template.yaml" /tmp/%[1]s
ln -s "/usr/local/cluster-profiles/$CLUSTER_TYPE" /tmp/%[1]s-cluster-profile
export CLUSTER_TYPE
exec ci-operator \
    --artifact-dir=$(ARTIFACTS) \
    --give-pr-author-access-to-namespace=true \
    --secret-dir=/tmp/%[1]s-cluster-profile \
    --sentry-dsn-path=/etc/sentry-dsn/ci-operator \
    --target=%[1]s \
    --template=/tmp/%[1]s
`

	oauthTokenPath  = "/usr/local/github-credentials"
	oauthSecretName = "github-credentials-openshift-ci-robot-private-git-cloner"
	oauthKey        = "oauth"

	prowgenConfigFile = ".config.prowgen"
)

var (
	openshiftInstallerRandomProfiles = []cioperatorapi.ClusterProfile{
		cioperatorapi.ClusterProfileAWS,
		cioperatorapi.ClusterProfileAzure4,
		cioperatorapi.ClusterProfileVSphere,
	}
)

type options struct {
	fromDir         string
	fromReleaseRepo bool

	toDir         string
	toReleaseRepo bool

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromDir, "from-dir", "", "Path to a directory with a directory structure holding ci-operator configuration files for multiple components")
	flag.BoolVar(&opt.fromReleaseRepo, "from-release-repo", false, "If set, it behaves like --from-dir=$GOPATH/src/github.com/openshift/release/ci-operator/config")

	flag.StringVar(&opt.toDir, "to-dir", "", "Path to a directory with a directory structure holding Prow job configuration files for multiple components")
	flag.BoolVar(&opt.toReleaseRepo, "to-release-repo", false, "If set, it behaves like --to-dir=$GOPATH/src/github.com/openshift/release/ci-operator/jobs")

	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	return opt
}

func (o *options) process() error {
	var err error

	if o.fromReleaseRepo {
		if o.fromDir, err = getReleaseRepoDir("ci-operator/config"); err != nil {
			return fmt.Errorf("--from-release-repo error: %v", err)
		}
	}

	if o.toReleaseRepo {
		if o.toDir, err = getReleaseRepoDir("ci-operator/jobs"); err != nil {
			return fmt.Errorf("--to-release-repo error: %v", err)
		}
	}

	if o.fromDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--from-{dir,release-repo}` options")
	}

	if o.toDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{dir,release-repo}` options")
	}

	return nil
}

// Generate a PodSpec that runs `ci-operator`, to be used in Presubmit/Postsubmit
// Various pieces are derived from `org`, `repo`, `branch` and `target`.
// `additionalArgs` are passed as additional arguments to `ci-operator`
func generatePodSpec(info *prowgenInfo, secrets []*cioperatorapi.Secret) *kubeapi.PodSpec {
	configMapKeyRef := kubeapi.EnvVarSource{
		ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
			LocalObjectReference: kubeapi.LocalObjectReference{
				Name: info.ConfigMapName(),
			},
			Key: info.Basename(),
		},
	}
	volumeMounts := []kubeapi.VolumeMount{{
		Name:      sentryDsnMountName,
		MountPath: sentryDsnMountPath,
		ReadOnly:  true,
	}}

	if migrate.Migrated(info.Org, info.Repo, info.Branch) {
		volumeMounts = []kubeapi.VolumeMount{
			{
				Name:      sentryDsnMountName,
				MountPath: sentryDsnMountPath,
				ReadOnly:  true,
			},
			{
				Name:      "apici-ci-operator-credentials",
				MountPath: "/etc/apici",
				ReadOnly:  true,
			},
			{
				Name:      "pull-secret",
				MountPath: "/etc/pull-secret",
				ReadOnly:  true,
			},
		}
	}

	volumes := []kubeapi.Volume{{
		Name: sentryDsnMountName,
		VolumeSource: kubeapi.VolumeSource{
			Secret: &kubeapi.SecretVolumeSource{SecretName: sentryDsnSecretName},
		},
	}}

	if migrate.Migrated(info.Org, info.Repo, info.Branch) {
		volumes = []kubeapi.Volume{
			{
				Name: sentryDsnMountName,
				VolumeSource: kubeapi.VolumeSource{
					Secret: &kubeapi.SecretVolumeSource{SecretName: sentryDsnSecretName},
				},
			},
			{
				Name: "apici-ci-operator-credentials",
				VolumeSource: kubeapi.VolumeSource{
					Secret: &kubeapi.SecretVolumeSource{
						SecretName: "apici-ci-operator-credentials",
						Items: []kubeapi.KeyToPath{
							{
								Key:  "sa.ci-operator.apici.config",
								Path: "kubeconfig",
							},
						},
					},
				},
			},
			{
				Name: "pull-secret",
				VolumeSource: kubeapi.VolumeSource{
					Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
				},
			},
		}
	}

	for _, secret := range secrets {
		volumeMounts = append(volumeMounts, kubeapi.VolumeMount{
			Name:      secret.Name,
			MountPath: secret.MountPath,
			ReadOnly:  true,
		})

		volumes = append(volumes, kubeapi.Volume{
			Name: secret.Name,
			VolumeSource: kubeapi.VolumeSource{
				Secret: &kubeapi.SecretVolumeSource{SecretName: secret.Name},
			},
		})
	}

	if info.config.Private {
		volumes = append(volumes, kubeapi.Volume{
			Name: oauthSecretName,
			VolumeSource: kubeapi.VolumeSource{
				Secret: &kubeapi.SecretVolumeSource{SecretName: oauthSecretName},
			},
		})

		volumeMounts = append(volumeMounts, kubeapi.VolumeMount{
			Name:      oauthSecretName,
			MountPath: oauthTokenPath,
			ReadOnly:  true,
		})
	}

	return &kubeapi.PodSpec{
		ServiceAccountName: "ci-operator",
		Containers: []kubeapi.Container{
			{
				Image:           "ci-operator:latest",
				ImagePullPolicy: kubeapi.PullAlways,
				Env:             []kubeapi.EnvVar{{Name: "CONFIG_SPEC", ValueFrom: &configMapKeyRef}},
				Resources: kubeapi.ResourceRequirements{
					Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
				},
				VolumeMounts: volumeMounts,
			},
		},
		Volumes: volumes,
	}
}

func generateCiOperatorPodSpec(info *prowgenInfo, secrets []*cioperatorapi.Secret, targets []string, additionalArgs ...string) *kubeapi.PodSpec {
	for _, arg := range additionalArgs {
		if !strings.HasPrefix(arg, "--") {
			panic(fmt.Sprintf("all args to ci-operator must be in the form --flag=value, not %s", arg))
		}
	}

	ret := generatePodSpec(info, secrets)
	ret.Containers[0].Command = []string{"ci-operator"}
	ret.Containers[0].Args = append([]string{
		"--give-pr-author-access-to-namespace=true",
		"--artifact-dir=$(ARTIFACTS)",
		fmt.Sprintf("--sentry-dsn-path=%s", sentryDsnSecretPath),
		"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
		fmt.Sprintf("--org=%s", info.Org),
		fmt.Sprintf("--repo=%s", info.Repo),
		fmt.Sprintf("--branch=%s", info.Branch),
	}, additionalArgs...)

	if migrate.Migrated(info.Org, info.Repo, info.Branch) {
		ret.Containers[0].Args = append([]string{
			"--give-pr-author-access-to-namespace=true",
			"--artifact-dir=$(ARTIFACTS)",
			fmt.Sprintf("--sentry-dsn-path=%s", sentryDsnSecretPath),
			"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
			fmt.Sprintf("--org=%s", info.Org),
			fmt.Sprintf("--repo=%s", info.Repo),
			fmt.Sprintf("--branch=%s", info.Branch),
			"--kubeconfig=/etc/apici/kubeconfig",
			"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
		}, additionalArgs...)
	}
	for _, target := range targets {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--target=%s", target))
	}
	if info.config.Private {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--oauth-token-path=%s", filepath.Join(oauthTokenPath, oauthKey)))
	}
	for _, secret := range secrets {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--secret-dir=%s", secret.MountPath))
	}

	if len(info.Variant) > 0 {
		ret.Containers[0].Args = append(ret.Containers[0].Args, fmt.Sprintf("--variant=%s", info.Variant))
	}
	return ret
}

func generatePodSpecTemplate(info *prowgenInfo, release string, test *cioperatorapi.TestStepConfiguration) *kubeapi.PodSpec {
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
	} else if conf := test.OpenshiftAnsibleUpgradeClusterTestConfiguration; conf != nil {
		template = "cluster-launch-e2e-upgrade"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
	} else if conf := test.OpenshiftAnsible40ClusterTestConfiguration; conf != nil {
		template = "cluster-scaleup-e2e-40"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
		needsLeaseServer = true
	} else if conf := test.OpenshiftInstallerClusterTestConfiguration; conf != nil {
		if !conf.Upgrade {
			template = "cluster-launch-installer-e2e"
		}
		clusterProfile = conf.ClusterProfile
		needsLeaseServer = true
	} else if conf := test.OpenshiftInstallerSrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-src"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerUPIClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-upi-e2e"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerUPISrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-upi-src"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerConsoleClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-console"
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-custom-test-image"
		needsLeaseServer = true
		clusterProfile = conf.ClusterProfile
		testImageStreamTag = conf.From
	}
	var targetCloud string
	switch clusterProfile {
	case cioperatorapi.ClusterProfileAWS, cioperatorapi.ClusterProfileAWSAtomic, cioperatorapi.ClusterProfileAWSCentos, cioperatorapi.ClusterProfileAWSCentos40, cioperatorapi.ClusterProfileAWSGluster:
		targetCloud = "aws"
	case cioperatorapi.ClusterProfileAzure4:
		targetCloud = "azure4"
	case cioperatorapi.ClusterProfileGCP, cioperatorapi.ClusterProfileGCP40, cioperatorapi.ClusterProfileGCPHA,
		cioperatorapi.ClusterProfileGCPCRIO, cioperatorapi.ClusterProfileGCPLogging, cioperatorapi.ClusterProfileGCPLoggingJournald,
		cioperatorapi.ClusterProfileGCPLoggingJSONFile, cioperatorapi.ClusterProfileGCPLoggingCRIO:
		targetCloud = "gcp"
	case cioperatorapi.ClusterProfileOpenStack:
		targetCloud = "openstack"
	case cioperatorapi.ClusterProfileOvirt:
		targetCloud = "ovirt"
	case cioperatorapi.ClusterProfileVSphere:
		targetCloud = "vsphere"
	}
	clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", test.As)
	templatePath := fmt.Sprintf("/usr/local/%s", test.As)
	podSpec := generateCiOperatorPodSpec(info, test.Secrets, []string{test.As})
	clusterProfileVolume := generateClusterProfileVolume("cluster-profile", fmt.Sprintf("cluster-secrets-%s", targetCloud))
	switch clusterProfile {
	case cioperatorapi.ClusterProfileAWS, cioperatorapi.ClusterProfileAzure4, cioperatorapi.ClusterProfileOpenStack, cioperatorapi.ClusterProfileVSphere:
	default:
		clusterProfileVolume.VolumeSource.Projected.Sources = append(clusterProfileVolume.VolumeSource.Projected.Sources, kubeapi.VolumeProjection{
			ConfigMap: &kubeapi.ConfigMapProjection{
				LocalObjectReference: kubeapi.LocalObjectReference{
					Name: fmt.Sprintf("cluster-profile-%s", clusterProfile),
				},
			},
		})
	}
	if len(template) > 0 {
		podSpec.Volumes = append(podSpec.Volumes, generateConfigMapVolume("job-definition", []string{fmt.Sprintf("prow-job-%s", template)}))
	}
	podSpec.Volumes = append(podSpec.Volumes, clusterProfileVolume)
	container := &podSpec.Containers[0]
	container.Args = append(container.Args, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
	if len(template) > 0 {
		container.Args = append(container.Args, fmt.Sprintf("--template=%s", templatePath))
	}
	// TODO add to all templates when they are migrated
	// TODO expose boskos (behind an oauth proxy) so it can be used by build clusters
	if needsLeaseServer {
		if migrate.Migrated(info.Org, info.Repo, info.Branch) {
			container.Args = append(container.Args, "--lease-server=https://boskos-ci.svc.ci.openshift.org")
			container.Args = append(container.Args, "--lease-server-username=ci")
			container.Args = append(container.Args, "--lease-server-password-file=/etc/boskos/password")
			container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{
				Name:      "boskos",
				MountPath: "/etc/boskos",
				ReadOnly:  true,
			})
			podSpec.Volumes = append(podSpec.Volumes, kubeapi.Volume{
				Name: "boskos",
				VolumeSource: kubeapi.VolumeSource{
					Secret: &kubeapi.SecretVolumeSource{
						SecretName: "boskos-credentials",
						Items: []kubeapi.KeyToPath{
							{
								Key:  "password",
								Path: "password",
							},
						},
					},
				},
			})
		} else {
			container.Args = append(container.Args, "--lease-server=http://boskos")
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{Name: "cluster-profile", MountPath: clusterProfilePath})
	if len(template) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{Name: "job-definition", MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", template)})
		container.Env = append(
			container.Env,
			kubeapi.EnvVar{Name: "CLUSTER_TYPE", Value: targetCloud},
			kubeapi.EnvVar{Name: "JOB_NAME_SAFE", Value: strings.Replace(test.As, "_", "-", -1)},
			kubeapi.EnvVar{Name: "TEST_COMMAND", Value: test.Commands})
		if len(testImageStreamTag) > 0 {
			container.Env = append(container.Env,
				kubeapi.EnvVar{Name: "TEST_IMAGESTREAM_TAG", Value: testImageStreamTag})
		}
	}
	if needsReleaseRpms && (info.Org != "openshift" || info.Repo != "origin") {
		var repoPath = fmt.Sprintf("https://rpms.svc.ci.openshift.org/openshift-origin-v%s/", release)
		if strings.HasPrefix(release, "origin-v") {
			repoPath = fmt.Sprintf("https://rpms.svc.ci.openshift.org/openshift-%s/", release)
		}
		container.Env = append(container.Env, kubeapi.EnvVar{
			Name:  "RPM_REPO_OPENSHIFT_ORIGIN",
			Value: repoPath,
		})
	}
	if conf := test.OpenshiftAnsible40ClusterTestConfiguration; conf != nil {
		container.Env = append(
			container.Env,
			kubeapi.EnvVar{
				Name:  "RPM_REPO_CRIO_DIR",
				Value: fmt.Sprintf("%s-rhel-7", release)},
		)
	}
	if conf := test.OpenshiftAnsibleUpgradeClusterTestConfiguration; conf != nil {
		container.Env = append(
			container.Env,
			kubeapi.EnvVar{Name: "PREVIOUS_ANSIBLE_VERSION",
				Value: conf.PreviousVersion},
			kubeapi.EnvVar{Name: "PREVIOUS_IMAGE_ANSIBLE",
				Value: fmt.Sprintf("docker.io/openshift/origin-ansible:v%s", conf.PreviousVersion)},
			kubeapi.EnvVar{Name: "PREVIOUS_RPM_DEPENDENCIES_REPO",
				Value: conf.PreviousRPMDeps},
			kubeapi.EnvVar{Name: "PREVIOUS_RPM_REPO",
				Value: fmt.Sprintf("https://rpms.svc.ci.openshift.org/openshift-origin-v%s/", conf.PreviousVersion)})
	}
	return podSpec
}

func generatePodSpecRandom(info *prowgenInfo, test *cioperatorapi.TestStepConfiguration) *kubeapi.PodSpec {
	podSpec := generatePodSpec(info, test.Secrets)
	for _, p := range openshiftInstallerRandomProfiles {
		podSpec.Volumes = append(podSpec.Volumes, generateClusterProfileVolume("cluster-profile-"+string(p), "cluster-secrets-"+string(p)))
	}
	podSpec.Volumes = append(podSpec.Volumes, generateConfigMapVolume("job-definition", []string{"prow-job-cluster-launch-installer-e2e", "prow-job-cluster-launch-installer-upi-e2e"}))
	podSpec.Volumes = append(podSpec.Volumes, generateConfigMapVolume("e2e-targets", []string{"e2e-targets"}))
	container := &podSpec.Containers[0]
	container.Command = []string{"bash"}
	container.Args = []string{"-c", fmt.Sprintf(openshiftInstallerRandomCmd, test.As)}
	container.Env = append(container.Env, []kubeapi.EnvVar{
		{Name: "JOB_NAME_SAFE", Value: strings.Replace(test.As, "_", "-", -1)},
		{Name: "TEST_COMMAND", Value: test.Commands},
	}...)
	for _, p := range openshiftInstallerRandomProfiles {
		container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{
			Name:      "cluster-profile-" + string(p),
			MountPath: "/usr/local/cluster-profiles/" + string(p),
		})
	}
	container.VolumeMounts = append(container.VolumeMounts, []kubeapi.VolumeMount{{
		Name:      "e2e-targets",
		MountPath: "/usr/local/e2e-targets",
		SubPath:   "e2e-targets",
	}, {
		Name:      "job-definition",
		MountPath: "/usr/local/job-definition"},
	}...)
	return podSpec
}

func generateClusterProfileVolume(name, profile string) kubeapi.Volume {
	return kubeapi.Volume{
		Name: name,
		VolumeSource: kubeapi.VolumeSource{
			Projected: &kubeapi.ProjectedVolumeSource{
				Sources: []kubeapi.VolumeProjection{{
					Secret: &kubeapi.SecretProjection{
						LocalObjectReference: kubeapi.LocalObjectReference{
							Name: profile,
						},
					}},
				},
			},
		},
	}
}

func generateConfigMapVolume(name string, templates []string) kubeapi.Volume {
	ret := kubeapi.Volume{Name: name}
	switch len(templates) {
	case 0:
	case 1:
		ret.VolumeSource = kubeapi.VolumeSource{
			ConfigMap: &kubeapi.ConfigMapVolumeSource{
				LocalObjectReference: kubeapi.LocalObjectReference{
					Name: templates[0],
				},
			},
		}
	default:
		ret.VolumeSource = kubeapi.VolumeSource{
			Projected: &kubeapi.ProjectedVolumeSource{},
		}
		s := &ret.VolumeSource.Projected.Sources
		for _, t := range templates {
			*s = append(*s, kubeapi.VolumeProjection{
				ConfigMap: &kubeapi.ConfigMapProjection{
					LocalObjectReference: kubeapi.LocalObjectReference{
						Name: t,
					},
				},
			})
		}
	}
	return ret
}

func generateJobBase(name, prefix string, info *prowgenInfo, label jc.ProwgenLabel, podSpec *kubeapi.PodSpec, rehearsable bool, pathAlias *string) prowconfig.JobBase {
	labels := map[string]string{jc.ProwJobLabelGenerated: string(label)}

	if rehearsable {
		labels[jc.CanBeRehearsedLabel] = string(jc.Generated)
	}

	jobName := info.Info.JobName(prefix, name)
	if len(info.Variant) > 0 {
		labels[prowJobLabelVariant] = info.Variant
	}
	newTrue := true
	dc := &v1.DecorationConfig{SkipCloning: &newTrue}
	base := prowconfig.JobBase{
		Agent:  string(v1.KubernetesAgent),
		Labels: labels,
		Name:   jobName,
		Spec:   podSpec,
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: dc,
			Decorate:         true,
		},
	}
	if pathAlias != nil {
		base.PathAlias = *pathAlias
	}
	if info.config.Private {
		base.Hidden = true
	}
	return base
}

func generatePresubmitForTest(name string, info *prowgenInfo, label jc.ProwgenLabel, podSpec *kubeapi.PodSpec, rehearsable bool, pathAlias *string) *prowconfig.Presubmit {
	if len(info.Variant) > 0 {
		name = fmt.Sprintf("%s-%s", info.Variant, name)
	}
	base := generateJobBase(name, jobconfig.PresubmitPrefix, info, label, podSpec, rehearsable, pathAlias)
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: true,
		Brancher:  prowconfig.Brancher{Branches: []string{info.Branch}},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/prow/%s", name),
		},
		RerunCommand: prowconfig.DefaultRerunCommandFor(name),
		Trigger:      prowconfig.DefaultTriggerFor(name),
	}
}

func generatePostsubmitForTest(name string, info *prowgenInfo, label jc.ProwgenLabel, podSpec *kubeapi.PodSpec, pathAlias *string) *prowconfig.Postsubmit {
	if len(info.Variant) > 0 {
		name = fmt.Sprintf("%s-%s", info.Variant, name)
	}
	base := generateJobBase(name, jobconfig.PostsubmitPrefix, info, label, podSpec, false, pathAlias)
	return &prowconfig.Postsubmit{
		JobBase:  base,
		Brancher: prowconfig.Brancher{Branches: []string{makeBranchExplicit(info.Branch)}},
	}
}

func generatePeriodicForTest(name string, info *prowgenInfo, label jc.ProwgenLabel, podSpec *kubeapi.PodSpec, rehearsable bool, cron string, pathAlias *string) *prowconfig.Periodic {
	if len(info.Variant) > 0 {
		name = fmt.Sprintf("%s-%s", info.Variant, name)
	}
	base := generateJobBase(name, jobconfig.PeriodicPrefix, info, label, podSpec, rehearsable, nil)
	// periodics are not associated with a repo per se, but we can add in an
	// extra ref so that periodics which want to access the repo tha they are
	// defined for can have that information
	ref := v1.Refs{
		Org:     info.Org,
		Repo:    info.Repo,
		BaseRef: info.Branch,
	}
	if pathAlias != nil {
		ref.PathAlias = *pathAlias
	}
	base.ExtraRefs = append([]v1.Refs{ref}, base.ExtraRefs...)
	return &prowconfig.Periodic{
		JobBase: base,
		Cron:    cron,
	}
}

// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additional
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
func generateJobs(
	configSpec *cioperatorapi.ReleaseBuildConfiguration, info *prowgenInfo, label jc.ProwgenLabel,
) *prowconfig.JobConfig {

	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}
	var periodics []prowconfig.Periodic

	for _, element := range configSpec.Tests {
		var podSpec *kubeapi.PodSpec
		if element.Secret != nil {
			element.Secrets = append(element.Secrets, element.Secret)
		}
		if element.ContainerTestConfiguration != nil {
			podSpec = generateCiOperatorPodSpec(info, element.Secrets, []string{element.As})
		} else {
			var release string
			if c := configSpec.ReleaseTagConfiguration; c != nil {
				release = c.Name
			}
			if conf := element.OpenshiftInstallerRandomClusterTestConfiguration; conf != nil {
				podSpec = generatePodSpecRandom(info, &element)
			} else {
				podSpec = generatePodSpecTemplate(info, release, &element)
			}
		}

		if element.Cron == nil {
			presubmit := *generatePresubmitForTest(element.As, info, label, podSpec, true, configSpec.CanonicalGoRepository)
			presubmit.Cluster = migrate.GetBuildClusterForPresubmit(info.Org, info.Repo, info.Branch)
			presubmits[orgrepo] = append(presubmits[orgrepo], presubmit)
		} else {
			periodic := *generatePeriodicForTest(element.As, info, label, podSpec, true, *element.Cron, configSpec.CanonicalGoRepository)
			periodic.Cluster = migrate.GetBuildClusterForPeriodic(info.Org, info.Repo, info.Branch)
			periodics = append(periodics, *generatePeriodicForTest(element.As, info, label, podSpec, true, *element.Cron, configSpec.CanonicalGoRepository))
		}
	}

	var imageTargets []string
	if configSpec.PromotionConfiguration != nil {
		for additional := range configSpec.PromotionConfiguration.AdditionalImages {
			imageTargets = append(imageTargets, configSpec.PromotionConfiguration.AdditionalImages[additional])
		}
	}

	if len(configSpec.Images) > 0 || len(imageTargets) > 0 {
		imageTargets = append(imageTargets, "[images]")
	}

	if len(imageTargets) > 0 {
		// Identify which jobs need a to have a release payload explicitly requested
		var presubmitTargets = imageTargets
		if promotion.PromotesOfficialImages(configSpec) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		podSpec := generateCiOperatorPodSpec(info, nil, presubmitTargets)
		presubmit := *generatePresubmitForTest("images", info, label, podSpec, true, configSpec.CanonicalGoRepository)
		presubmit.Cluster = migrate.GetBuildClusterForPresubmit(info.Org, info.Repo, info.Branch)
		presubmits[orgrepo] = append(presubmits[orgrepo], presubmit)

		if configSpec.PromotionConfiguration != nil {
			podSpec := generateCiOperatorPodSpec(info, nil, imageTargets, []string{"--promote"}...)
			postsubmit := *generatePostsubmitForTest("images", info, label, podSpec, configSpec.CanonicalGoRepository)
			postsubmit.Cluster = migrate.GetBuildClusterForPostsubmit(info.Org, info.Repo, info.Branch)
			postsubmits[orgrepo] = append(postsubmits[orgrepo], postsubmit)
		}
	}

	return &prowconfig.JobConfig{
		PresubmitsStatic: presubmits,
		Postsubmits:      postsubmits,
		Periodics:        periodics,
	}
}

// Prowgen holds the information of the prowgen's configuration file.
type Config struct {
	Private bool `json:"private,omitempty"`
}

type prowgenInfo struct {
	config.Info
	config Config
}

func readProwgenConfig(path string) (*Config, error) {
	var pConfig *Config
	b, err := ioutil.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("prowgen config found in path %s but couldn't read the file: %v", path, err)
	}

	if err == nil {
		if err := yaml.Unmarshal(b, &pConfig); err != nil {
			return nil, fmt.Errorf("prowgen config found in path %sbut couldn't unmarshal it: %v", path, err)
		}
	}

	return pConfig, nil
}

// generateJobsToDir returns a callback that knows how to generate prow job configuration
// into the dir provided by consuming ci-operator configuration.
//
// Returned callback will cache Prowgen config reads, including unsuccessful attempts
// The keys are either `org` or `org/repo`, and if present in the cache, a previous
// execution of the callback already made an attempt to read a prowgen config in the
// appropriate location, and either stored a pointer to the parsed config if if was
// successfully read, or stored `nil` when the prowgen config could not be read (usually
// because the drop-in is not there).
func generateJobsToDir(dir string, label jc.ProwgenLabel) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	// Return a closure so the cache is shared among callback calls
	cache := map[string]*Config{}
	return func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		pInfo := &prowgenInfo{Info: *info, config: Config{Private: false}}
		var ok bool
		var err error
		var orgConfig, repoConfig *Config

		if orgConfig, ok = cache[info.Org]; !ok {
			if cache[info.Org], err = readProwgenConfig(filepath.Join(info.OrgPath, prowgenConfigFile)); err != nil {
				return err
			}
			orgConfig = cache[info.Org]
		}

		if repoConfig, ok = cache[orgRepo]; !ok {
			if cache[orgRepo], err = readProwgenConfig(filepath.Join(info.RepoPath, prowgenConfigFile)); err != nil {
				return err
			}
			repoConfig = cache[orgRepo]
		}

		switch {
		case orgConfig != nil:
			pInfo.config = *orgConfig
		case repoConfig != nil:
			pInfo.config = *repoConfig
		}

		return jc.WriteToDir(dir, info.Org, info.Repo, generateJobs(configSpec, pInfo, label))
	}
}

func getReleaseRepoDir(directory string) (string, error) {
	tentative := filepath.Join(build.Default.GOPATH, "src/github.com/openshift/release", directory)
	if stat, err := os.Stat(tentative); err == nil && stat.IsDir() {
		return tentative, nil
	}
	return "", fmt.Errorf("%s is not an existing directory", tentative)
}

// simpleBranchRegexp matches a branch name that does not appear to be a regex (lacks wildcard,
// group, or other modifiers). For instance, `master` is considered simple, `master-.*` would
// not.
var simpleBranchRegexp = regexp.MustCompile(`^[\w\-.]+$`)

// makeBranchExplicit updates the provided branch to prevent wildcard matches to the given branch
// if the branch value does not appear to contain an explicit regex pattern. I.e. 'master'
// is turned into '^master$'.
func makeBranchExplicit(branch string) string {
	if !simpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s$", regexp.QuoteMeta(branch))
}

func isStale(job prowconfig.JobBase) bool {
	genLabel, generated := job.Labels[jc.ProwJobLabelGenerated]
	return generated && genLabel != string(jc.New)
}

func isGenerated(job prowconfig.JobBase) bool {
	_, generated := job.Labels[jc.ProwJobLabelGenerated]
	return generated
}

func prune(jobConfig *prowconfig.JobConfig) *prowconfig.JobConfig {
	var pruned prowconfig.JobConfig

	for repo, jobs := range jobConfig.PresubmitsStatic {
		for _, job := range jobs {
			if isStale(job.JobBase) {
				continue
			}

			if isGenerated(job.JobBase) {
				job.Labels[jc.ProwJobLabelGenerated] = string(jc.Generated)
			}

			if pruned.PresubmitsStatic == nil {
				pruned.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
			}

			pruned.PresubmitsStatic[repo] = append(pruned.PresubmitsStatic[repo], job)
		}
	}

	for repo, jobs := range jobConfig.Postsubmits {
		for _, job := range jobs {
			if isStale(job.JobBase) {
				continue
			}
			if isGenerated(job.JobBase) {
				job.Labels[jc.ProwJobLabelGenerated] = string(jc.Generated)

			}
			if pruned.Postsubmits == nil {
				pruned.Postsubmits = map[string][]prowconfig.Postsubmit{}
			}

			pruned.Postsubmits[repo] = append(pruned.Postsubmits[repo], job)
		}
	}

	for _, job := range jobConfig.Periodics {
		if isStale(job.JobBase) {
			continue
		}
		if isGenerated(job.JobBase) {
			job.Labels[jc.ProwJobLabelGenerated] = string(jc.Generated)

		}

		pruned.Periodics = append(pruned.Periodics, job)
	}

	return &pruned
}

func pruneStaleJobs(jobDir, subDir string) error {
	if err := jc.OperateOnJobConfigSubdir(jobDir, subDir, func(jobConfig *prowconfig.JobConfig, info *jc.Info) error {
		pruned := prune(jobConfig)

		if len(pruned.PresubmitsStatic) == 0 && len(pruned.Postsubmits) == 0 && len(pruned.Periodics) == 0 {
			if err := os.Remove(info.Filename); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			if err := jc.WriteToFile(info.Filename, pruned); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	flagSet.Parse(os.Args[1:])

	if opt.help {
		flagSet.Usage()
		os.Exit(0)
	}

	if err := opt.process(); err != nil {
		logrus.WithError(err).Fatal("Failed to process arguments")
		os.Exit(1)
	}

	args := flagSet.Args()
	if len(args) == 0 {
		args = append(args, "")
	}
	genJobs := generateJobsToDir(opt.toDir, jc.New)
	for _, subDir := range args {
		if err := config.OperateOnCIOperatorConfigSubdir(opt.fromDir, subDir, genJobs); err != nil {
			fields := logrus.Fields{"target": opt.toDir, "source": opt.fromDir, "subdir": subDir}
			logrus.WithError(err).WithFields(fields).Fatal("Failed to generate jobs")
		}
		if err := pruneStaleJobs(opt.toDir, subDir); err != nil {
			fields := logrus.Fields{"target": opt.toDir, "source": opt.fromDir, "subdir": subDir}
			logrus.WithError(err).WithFields(fields).Fatal("Failed to prune stale generated jobs")
		}
	}
}
