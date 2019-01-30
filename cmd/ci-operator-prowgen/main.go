package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
	kubeapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	prowconfig "k8s.io/test-infra/prow/config"
	prowkube "k8s.io/test-infra/prow/kube"

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
func generatePodSpec(configFile, target string, additionalArgs ...string) *kubeapi.PodSpec {
	for _, arg := range additionalArgs {
		if !strings.HasPrefix(arg, "--") {
			panic(fmt.Sprintf("all args to ci-operator must be in the form --flag=value, not %s", arg))
		}
	}

	configMapKeyRef := kubeapi.EnvVarSource{
		ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
			LocalObjectReference: kubeapi.LocalObjectReference{
				Name: "ci-operator-configs",
			},
			Key: configFile,
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
					Limits:   kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(500, resource.DecimalSI)},
				},
			},
		},
	}
}

func generatePodSpecTemplate(org, repo, configFile, release string, test *cioperatorapi.TestStepConfiguration, additionalArgs ...string) *kubeapi.PodSpec {
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
		template = "cluster-launch-e2e-40"
		clusterProfile = conf.ClusterProfile
		needsReleaseRpms = true
	} else if conf := test.OpenshiftInstallerClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-e2e"
		clusterProfile = conf.ClusterProfile
	} else if conf := test.OpenshiftInstallerSrcClusterTestConfiguration; conf != nil {
		template = "cluster-launch-installer-src"
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
	}
	clusterProfilePath := fmt.Sprintf("/usr/local/%s-cluster-profile", test.As)
	templatePath := fmt.Sprintf("/usr/local/%s", test.As)
	podSpec := generatePodSpec(configFile, test.As, additionalArgs...)
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
	case cioperatorapi.ClusterProfileAWS, cioperatorapi.ClusterProfileOpenStack:
	default:
		clusterProfileVolume.VolumeSource.Projected.Sources = append(clusterProfileVolume.VolumeSource.Projected.Sources, kubeapi.VolumeProjection{
			ConfigMap: &kubeapi.ConfigMapProjection{
				LocalObjectReference: kubeapi.LocalObjectReference{
					Name: fmt.Sprintf("cluster-profile-%s", clusterProfile),
				},
			},
		})
	}
	podSpec.Volumes = []kubeapi.Volume{
		{
			Name: "job-definition",
			VolumeSource: kubeapi.VolumeSource{
				ConfigMap: &kubeapi.ConfigMapVolumeSource{
					LocalObjectReference: kubeapi.LocalObjectReference{
						Name: fmt.Sprintf("prow-job-%s", template),
					},
				},
			},
		},
		clusterProfileVolume,
	}
	container := &podSpec.Containers[0]
	container.Args = append(
		container.Args,
		fmt.Sprintf("--secret-dir=%s", clusterProfilePath),
		fmt.Sprintf("--template=%s", templatePath))
	container.VolumeMounts = []kubeapi.VolumeMount{
		{Name: "cluster-profile", MountPath: clusterProfilePath},
		{Name: "job-definition", MountPath: templatePath, SubPath: fmt.Sprintf("%s.yaml", template)},
	}
	container.Env = append(
		container.Env,
		kubeapi.EnvVar{Name: "CLUSTER_TYPE", Value: targetCloud},
		kubeapi.EnvVar{Name: "JOB_NAME_SAFE", Value: strings.Replace(test.As, "_", "-", -1)},
		kubeapi.EnvVar{Name: "TEST_COMMAND", Value: test.Commands})
	if needsReleaseRpms && (org != "openshift" || repo != "origin") {
		container.Env = append(container.Env, kubeapi.EnvVar{
			Name:  "RPM_REPO_OPENSHIFT_ORIGIN",
			Value: fmt.Sprintf("https://rpms.svc.ci.openshift.org/openshift-%s/", release),
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

// Generate a Presubmit job for the given parameters
func generatePresubmitForTest(name string, repoInfo *configFilePathElements, podSpec *kubeapi.PodSpec) *prowconfig.Presubmit {
	labels := make(map[string]string)

	jobPrefix := fmt.Sprintf("pull-ci-%s-%s-%s-", repoInfo.org, repoInfo.repo, repoInfo.branch)
	if len(repoInfo.variant) > 0 {
		name = fmt.Sprintf("%s-%s", repoInfo.variant, name)
		labels[prowJobLabelVariant] = repoInfo.variant
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
		Brancher:     prowconfig.Brancher{Branches: []string{repoInfo.branch}},
		Context:      fmt.Sprintf("ci/prow/%s", name),
		RerunCommand: fmt.Sprintf("/test %s", name),
		Trigger:      fmt.Sprintf(`((?m)^/test( all| %s),?(\s+|$))`, name),
	}
}

// Generate a Presubmit job for the given parameters
func generatePostsubmitForTest(
	name string,
	repoInfo *configFilePathElements,
	treatBranchesAsExplicit bool,
	labels map[string]string,
	podSpec *kubeapi.PodSpec) *prowconfig.Postsubmit {

	copiedLabels := make(map[string]string)
	for k, v := range labels {
		copiedLabels[k] = v
	}

	branchName := jc.MakeRegexFilenameLabel(repoInfo.branch)
	jobPrefix := fmt.Sprintf("branch-ci-%s-%s-%s-", repoInfo.org, repoInfo.repo, branchName)
	if len(repoInfo.variant) > 0 {
		name = fmt.Sprintf("%s-%s", repoInfo.variant, name)
		copiedLabels[prowJobLabelVariant] = repoInfo.variant
	}
	jobName := fmt.Sprintf("%s%s", jobPrefix, name)
	if len(jobName) > 63 && len(jobPrefix) < 53 {
		// warn if the prefix gives people enough space to choose names and they've chosen something long
		logrus.WithField("name", jobName).Warn("Generated job name is longer than 63 characters. This may cause issues when Prow attempts to label resources with job name. Consider a shorter name.")
	}

	branch := repoInfo.branch
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

func extractPromotionNamespace(configSpec *cioperatorapi.ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Namespace != "" {
		return configSpec.PromotionConfiguration.Namespace
	}

	if configSpec.InputConfiguration.ReleaseTagConfiguration != nil &&
		configSpec.InputConfiguration.ReleaseTagConfiguration.Namespace != "" {
		return configSpec.InputConfiguration.ReleaseTagConfiguration.Namespace
	}

	return ""
}

func extractPromotionName(configSpec *cioperatorapi.ReleaseBuildConfiguration) string {
	if configSpec.PromotionConfiguration != nil && configSpec.PromotionConfiguration.Name != "" {
		return configSpec.PromotionConfiguration.Name
	}

	if configSpec.InputConfiguration.ReleaseTagConfiguration != nil &&
		configSpec.InputConfiguration.ReleaseTagConfiguration.Name != "" {
		return configSpec.InputConfiguration.ReleaseTagConfiguration.Name
	}

	return ""
}

func shouldBePromoted(branch, namespace, name string) bool {
	if namespace == "openshift" {
		switch name {
		case "origin-v4.0":
			return branch == "master" || branch == "openshift-4.0"
		}
		// TODO: release branches?
	}

	return true
}

// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additinal
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
func generateJobs(
	configSpec *cioperatorapi.ReleaseBuildConfiguration, repoInfo *configFilePathElements,
) *prowconfig.JobConfig {

	orgrepo := fmt.Sprintf("%s/%s", repoInfo.org, repoInfo.repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}

	for _, element := range configSpec.Tests {
		var podSpec *kubeapi.PodSpec
		if element.ContainerTestConfiguration != nil {
			podSpec = generatePodSpec(repoInfo.configFilename, element.As)
		} else {
			var release string
			if c := configSpec.ReleaseTagConfiguration; c != nil {
				release = c.Name
			}
			podSpec = generatePodSpecTemplate(repoInfo.org, repoInfo.repo, repoInfo.configFilename, release, &element)
		}
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(element.As, repoInfo, podSpec))
	}

	if len(configSpec.Images) > 0 {
		// If the images are promoted to 'openshift' namespace, we need to add
		// 'artifacts: images' label to the [images] postsubmit and also target
		// --target=[release:latest] for [images] presubmits.
		labels := map[string]string{}
		var additionalPresubmitArgs []string
		promotionNamespace := extractPromotionNamespace(configSpec)
		promotionName := extractPromotionName(configSpec)
		if promotionNamespace == "openshift" {
			labels["artifacts"] = "images"
			if promotionName == "origin-v4.0" {
				additionalPresubmitArgs = []string{"--target=[release:latest]"}
			}
		}

		additionalPostsubmitArgs := []string{"--promote"}
		if configSpec.PromotionConfiguration != nil {
			for additionalImage := range configSpec.PromotionConfiguration.AdditionalImages {
				additionalPostsubmitArgs = append(additionalPostsubmitArgs, fmt.Sprintf("--target=%s", configSpec.PromotionConfiguration.AdditionalImages[additionalImage]))
			}
		}

		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest("images", repoInfo, generatePodSpec(repoInfo.configFilename, "[images]", additionalPresubmitArgs...)))

		// If we have and explicit promotion config, let's respect that. Otherwise, validate if the branch matches promotion target
		if configSpec.PromotionConfiguration != nil || shouldBePromoted(repoInfo.branch, promotionNamespace, promotionName) {
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *generatePostsubmitForTest("images", repoInfo, true, labels, generatePodSpec(repoInfo.configFilename, "[images]", additionalPostsubmitArgs...)))
		}
	}

	return &prowconfig.JobConfig{
		Presubmits:  presubmits,
		Postsubmits: postsubmits,
	}
}

func readCiOperatorConfig(configFilePath string) (*cioperatorapi.ReleaseBuildConfiguration, error) {
	data, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read ci-operator config (%v)", err)
	}

	var configSpec *cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return nil, fmt.Errorf("failed to load ci-operator config (%v)", err)
	}

	if err := configSpec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ci-operator config: %v", err)
	}

	return configSpec, nil
}

// path to ci-operator configuration file encodes information about tested code
// .../$ORGANIZATION/$REPOSITORY/$BRANCH.$EXT
type configFilePathElements struct {
	org            string
	repo           string
	branch         string
	variant        string
	configFilename string
}

// We use the directory/file naming convention to encode useful information
// about component repository information.
// The convention for ci-operator config files in this repo:
// ci-operator/config/ORGANIZATION/COMPONENT/BRANCH.yaml
func extractRepoElementsFromPath(configFilePath string) (*configFilePathElements, error) {
	configSpecDir := filepath.Dir(configFilePath)
	repo := filepath.Base(configSpecDir)
	if repo == "." || repo == "/" {
		return nil, fmt.Errorf("could not extract repo from '%s' (expected path like '.../ORG/REPO/BRANCH.yaml", configFilePath)
	}

	org := filepath.Base(filepath.Dir(configSpecDir))
	if org == "." || org == "/" {
		return nil, fmt.Errorf("could not extract org from '%s' (expected path like '.../ORG/REPO/BRANCH.yaml", configFilePath)
	}

	fileName := filepath.Base(configFilePath)
	s := strings.TrimSuffix(fileName, filepath.Ext(configFilePath))
	branch := strings.TrimPrefix(s, fmt.Sprintf("%s-%s-", org, repo))

	var variant string
	if i := strings.LastIndex(branch, "__"); i != -1 {
		variant = branch[i+2:]
		branch = branch[:i]
	}

	return &configFilePathElements{org, repo, branch, variant, fileName}, nil
}

func generateProwJobsFromConfigFile(configFilePath string) (*prowconfig.JobConfig, *configFilePathElements, error) {
	configSpec, err := readCiOperatorConfig(configFilePath)
	if err != nil {
		return nil, nil, err
	}

	repoInfo, err := extractRepoElementsFromPath(configFilePath)
	if err != nil {
		return nil, nil, err
	}
	jobConfig := generateJobs(configSpec, repoInfo)

	return jobConfig, repoInfo, nil
}

func isConfigFile(path string, info os.FileInfo) bool {
	extension := filepath.Ext(path)
	return !info.IsDir() && (extension == ".yaml" || extension == ".yml" || extension == ".json")
}

// Iterate over all ci-operator config files under a given path and generate a
// Prow job configuration files for each one under a different path, mimicking
// the directory structure.
func generateJobsFromDirectory(configDir, jobDir string) error {
	ok := true
	filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.WithError(err).Error("Error encontered while generating Prow job config")
			ok = false
			return nil
		}
		if isConfigFile(path, info) {
			jobConfig, repoInfo, err := generateProwJobsFromConfigFile(path)
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to generate jobs from config file")
				ok = false
				return nil
			}
			if err = jc.WriteToDir(jobDir, repoInfo.org, repoInfo.repo, jobConfig); err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to write jobs")
				ok = false
				return nil
			}
		}
		return nil
	})
	if !ok {
		return fmt.Errorf("Failed to generate jobs from directory")
	}
	return nil
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
		jobConfig, repoInfo, err := generateProwJobsFromConfigFile(opt.fromFile)
		if err != nil {
			logrus.WithError(err).WithField("source-file", opt.fromFile).Fatal("Failed to generate jobs")
		}
		if err := jc.WriteToDir(opt.toDir, repoInfo.org, repoInfo.repo, jobConfig); err != nil {
			logrus.WithError(err).WithField("target-dir", opt.toDir).Fatal("Failed to write jobs to directory")
		}
	} else { // from directory
		if err := generateJobsFromDirectory(opt.fromDir, opt.toDir); err != nil {
			fields := logrus.Fields{"target-dir": opt.toDir, "source-dir": opt.fromDir}
			logrus.WithError(err).WithFields(fields).Fatal("Failed to generate jobs")
		}
	}
}
