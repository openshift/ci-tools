package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
	kubeapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowconfig "k8s.io/test-infra/prow/config"
	prowkube "k8s.io/test-infra/prow/kube"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	jc "github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
)

const (
	prowJobLabelVariant = "ci-operator.openshift.io/variant"
)

type options struct {
	fromFile        string
	fromDir         string
	fromReleaseRepo bool

	toDir         string
	toReleaseRepo bool

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromFile, "from-file", "", "Path to a ci-operator configuration file")
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

	if (o.fromFile == "" && o.fromDir == "") || (o.fromFile != "" && o.fromDir != "") {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--from-{file,dir,release-repo}` options")
	}

	if o.toDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{dir,release-repo}` options")
	}

	return nil
}

// Generate a PodSpec that runs `ci-operator`, to be used in Presubmit/Postsubmit
// Various pieces are derived from `org`, `repo`, `branch` and `target`.
// `additionalArgs` are passed as additional arguments to `ci-operator`
func generatePodSpec(info *config.Info, target string, additionalArgs ...string) *kubeapi.PodSpec {
	for _, arg := range additionalArgs {
		if !strings.HasPrefix(arg, "--") {
			panic(fmt.Sprintf("all args to ci-operator must be in the form --flag=value, not %s", arg))
		}
	}

	configMapKeyRef := kubeapi.EnvVarSource{
		ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
			LocalObjectReference: kubeapi.LocalObjectReference{
				Name: info.ConfigMapName(),
			},
			Key: info.Basename(),
		},
	}

	return &kubeapi.PodSpec{
		ServiceAccountName: "ci-operator",
		Containers: []kubeapi.Container{
			{
				Image:           "ci-operator:latest",
				ImagePullPolicy: kubeapi.PullAlways,
				Command:         []string{"ci-operator"},
				Args:            append([]string{"--give-pr-author-access-to-namespace=true", "--artifact-dir=$(ARTIFACTS)", fmt.Sprintf("--target=%s", target)}, additionalArgs...),
				Env:             []kubeapi.EnvVar{{Name: "CONFIG_SPEC", ValueFrom: &configMapKeyRef}},
				Resources: kubeapi.ResourceRequirements{
					Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
				},
			},
		},
	}
}

func generatePodSpecTemplate(info *config.Info, release string, test *cioperatorapi.TestStepConfiguration, additionalArgs ...string) *kubeapi.PodSpec {
	var template string
	var clusterProfile cioperatorapi.ClusterProfile
	var needsReleaseRpms bool
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
	} else if conf := test.OpenshiftInstallerClusterTestConfiguration; conf != nil {
		if !conf.Upgrade {
			template = "cluster-launch-installer-e2e"
		}
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerSrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-src"
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerUPIClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-upi-e2e"
		clusterProfile = conf.ClusterProfile
	}
	var targetCloud string
	switch clusterProfile {
	case cioperatorapi.ClusterProfileAWS, cioperatorapi.ClusterProfileAWSAtomic, cioperatorapi.ClusterProfileAWSCentos, cioperatorapi.ClusterProfileAWSCentos40, cioperatorapi.ClusterProfileAWSGluster:
		targetCloud = "aws"
	case cioperatorapi.ClusterProfileGCP, cioperatorapi.ClusterProfileGCP40, cioperatorapi.ClusterProfileGCPHA,
		cioperatorapi.ClusterProfileGCPCRIO, cioperatorapi.ClusterProfileGCPLogging, cioperatorapi.ClusterProfileGCPLoggingJournald,
		cioperatorapi.ClusterProfileGCPLoggingJSONFile, cioperatorapi.ClusterProfileGCPLoggingCRIO:
		targetCloud = "gcp"
	case cioperatorapi.ClusterProfileOpenStack:
		targetCloud = "openstack"
	case cioperatorapi.ClusterProfileVSphere:
		targetCloud = "vsphere"
	}
	clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", test.As)
	templatePath := fmt.Sprintf("/usr/local/%s", test.As)
	podSpec := generatePodSpec(info, test.As, additionalArgs...)
	clusterProfileVolume := kubeapi.Volume{
		Name: "cluster-profile",
		VolumeSource: kubeapi.VolumeSource{
			Projected: &kubeapi.ProjectedVolumeSource{
				Sources: []kubeapi.VolumeProjection{
					{
						Secret: &kubeapi.SecretProjection{
							LocalObjectReference: kubeapi.LocalObjectReference{
								Name: fmt.Sprintf("cluster-secrets-%s", targetCloud),
							},
						},
					},
				},
			},
		},
	}
	switch clusterProfile {
	case cioperatorapi.ClusterProfileAWS, cioperatorapi.ClusterProfileOpenStack, cioperatorapi.ClusterProfileVSphere:
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
		podSpec.Volumes = append(podSpec.Volumes, kubeapi.Volume{
			Name: "job-definition",
			VolumeSource: kubeapi.VolumeSource{
				ConfigMap: &kubeapi.ConfigMapVolumeSource{
					LocalObjectReference: kubeapi.LocalObjectReference{
						Name: fmt.Sprintf("prow-job-%s", template),
					},
				},
			},
		})
	}
	podSpec.Volumes = append(podSpec.Volumes, clusterProfileVolume)
	container := &podSpec.Containers[0]
	container.Args = append(container.Args, fmt.Sprintf("--secret-dir=%s", clusterProfilePath))
	if len(template) > 0 {
		container.Args = append(container.Args, fmt.Sprintf("--template=%s", templatePath))
	}
	container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{Name: "cluster-profile", MountPath: clusterProfilePath})
	if len(template) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, kubeapi.VolumeMount{Name: "job-definition", MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", template)})
		container.Env = append(
			container.Env,
			kubeapi.EnvVar{Name: "CLUSTER_TYPE", Value: targetCloud},
			kubeapi.EnvVar{Name: "JOB_NAME_SAFE", Value: strings.Replace(test.As, "_", "-", -1)},
			kubeapi.EnvVar{Name: "TEST_COMMAND", Value: test.Commands})
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

func generatePresubmitForTest(name string, info *config.Info, podSpec *kubeapi.PodSpec) *prowconfig.Presubmit {
	labels := map[string]string{jc.ProwJobLabelGenerated: jc.Generated}

	jobPrefix := fmt.Sprintf("pull-ci-%s-%s-%s-", info.Org, info.Repo, info.Branch)
	if len(info.Variant) > 0 {
		name = fmt.Sprintf("%s-%s", info.Variant, name)
		labels[prowJobLabelVariant] = info.Variant
	}
	jobName := fmt.Sprintf("%s%s", jobPrefix, name)
	if len(jobName) > 63 && len(jobPrefix) < 53 {
		// warn if the prefix gives people enough space to choose names and they've chosen something long
		logrus.WithField("name", jobName).Warn("Generated job name is longer than 63 characters. This may cause issues when Prow attempts to label resources with job name. Consider a shorter name.")
	}

	newTrue := true

	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Labels: labels,
			Name:   jobName,
			Spec:   podSpec,
			UtilityConfig: prowconfig.UtilityConfig{
				DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
				Decorate:         true,
			},
		},
		AlwaysRun:    true,
		Brancher:     prowconfig.Brancher{Branches: []string{info.Branch}},
		Context:      fmt.Sprintf("ci/prow/%s", name),
		RerunCommand: fmt.Sprintf("/test %s", name),
		Trigger:      fmt.Sprintf(`(?m)^/test (?:.*? )?%s(?: .*?)?$`, name),
	}
}

func generatePostsubmitForTest(
	name string,
	info *config.Info,
	treatBranchesAsExplicit bool,
	labels map[string]string,
	podSpec *kubeapi.PodSpec) *prowconfig.Postsubmit {

	copiedLabels := make(map[string]string)
	for k, v := range labels {
		copiedLabels[k] = v
	}
	copiedLabels[jc.ProwJobLabelGenerated] = jc.Generated

	branchName := jc.MakeRegexFilenameLabel(info.Branch)
	jobPrefix := fmt.Sprintf("branch-ci-%s-%s-%s-", info.Org, info.Repo, branchName)
	if len(info.Variant) > 0 {
		name = fmt.Sprintf("%s-%s", info.Variant, name)
		copiedLabels[prowJobLabelVariant] = info.Variant
	}
	jobName := fmt.Sprintf("%s%s", jobPrefix, name)
	if len(jobName) > 63 && len(jobPrefix) < 53 {
		// warn if the prefix gives people enough space to choose names and they've chosen something long
		logrus.WithField("name", jobName).Warn("Generated job name is longer than 63 characters. This may cause issues when Prow attempts to label resources with job name. Consider a shorter name.")
	}

	branch := info.Branch
	if treatBranchesAsExplicit {
		branch = makeBranchExplicit(branch)
	}

	newTrue := true

	return &prowconfig.Postsubmit{
		JobBase: prowconfig.JobBase{
			Agent:  "kubernetes",
			Name:   jobName,
			Spec:   podSpec,
			Labels: copiedLabels,
			UtilityConfig: prowconfig.UtilityConfig{
				DecorationConfig: &prowkube.DecorationConfig{SkipCloning: &newTrue},
				Decorate:         true,
			},
		},
		Brancher: prowconfig.Brancher{Branches: []string{branch}},
	}
}

// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additinal
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
func generateJobs(
	configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info,
) *prowconfig.JobConfig {

	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}

	for _, element := range configSpec.Tests {
		var podSpec *kubeapi.PodSpec
		if element.ContainerTestConfiguration != nil {
			podSpec = generatePodSpec(info, element.As)
		} else {
			var release string
			if c := configSpec.ReleaseTagConfiguration; c != nil {
				release = c.Name
			}
			podSpec = generatePodSpecTemplate(info, release, &element)
		}
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(element.As, info, podSpec))
	}

	if len(configSpec.Images) > 0 {
		// TODO: we should populate labels based on ci-operator characteristics
		labels := map[string]string{}

		// Identify which jobs need a to have a release payload explicitly requested
		var additionalPresubmitArgs []string
		if promotion.PromotesOfficialImages(configSpec) {
			additionalPresubmitArgs = []string{"--target=[release:latest]"}
		}

		additionalPostsubmitArgs := []string{"--promote"}
		if configSpec.PromotionConfiguration != nil {
			for additionalImage := range configSpec.PromotionConfiguration.AdditionalImages {
				additionalPostsubmitArgs = append(additionalPostsubmitArgs, fmt.Sprintf("--target=%s", configSpec.PromotionConfiguration.AdditionalImages[additionalImage]))
			}
		}

		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest("images", info, generatePodSpec(info, "[images]", additionalPresubmitArgs...)))

		if configSpec.PromotionConfiguration != nil {
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *generatePostsubmitForTest("images", info, true, labels, generatePodSpec(info, "[images]", additionalPostsubmitArgs...)))
		}
	}

	return &prowconfig.JobConfig{
		Presubmits:  presubmits,
		Postsubmits: postsubmits,
	}
}

// generateJobsToDir returns a callback that knows how to generate prow job configuration
// into the dir provided by consuming ci-operator configuration
func generateJobsToDir(dir string) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	return func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		return jc.WriteToDir(dir, info.Org, info.Repo, generateJobs(configSpec, info))
	}
}

func getReleaseRepoDir(directory string) (string, error) {
	var gopath string
	if gopath = os.Getenv("GOPATH"); len(gopath) == 0 {
		return "", fmt.Errorf("GOPATH not set, cannot infer openshift/release repo location")
	}
	tentative := filepath.Join(gopath, "src/github.com/openshift/release", directory)
	if stat, err := os.Stat(tentative); err == nil && stat.IsDir() {
		return tentative, nil
	}
	return "", fmt.Errorf("%s is not an existing directory", tentative)
}

// simpleBranchRegexp matches a branch name that does not appear to be a regex (lacks wildcard,
// group, or other modifiers). For instance, `master` is considered simple, `master-.*` would
// not.
var simpleBranchRegexp = regexp.MustCompile(`^[\w\-\.]+$`)

// makeBranchExplicit updates the provided branch to prevent wildcard matches to the given branch
// if the branch value does not appear to contain an explicit regex pattern. I.e. 'master'
// is turned into '^master$'.
func makeBranchExplicit(branch string) string {
	if !simpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s$", regexp.QuoteMeta(branch))
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

	if len(opt.fromFile) > 0 {
		if err := config.OperateOnCIOperatorConfig(opt.fromFile, generateJobsToDir(opt.toDir)); err != nil {
			logrus.WithError(err).WithField("source-file", opt.fromFile).Fatal("Failed to generate jobs")
		}
	} else { // from directory
		if err := config.OperateOnCIOperatorConfigDir(opt.fromDir, generateJobsToDir(opt.toDir)); err != nil {
			fields := logrus.Fields{"target-dir": opt.toDir, "source-dir": opt.fromDir}
			logrus.WithError(err).WithFields(fields).Fatal("Failed to generate jobs")
		}
	}
}
