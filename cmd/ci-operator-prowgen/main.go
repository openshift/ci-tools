package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"
	kubeapi "k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	prowkube "k8s.io/test-infra/prow/kube"

	jc "github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
)

type options struct {
	fromFile        string
	fromDir         string
	fromReleaseRepo bool

	toFile        string
	toDir         string
	toReleaseRepo bool

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromFile, "from-file", "", "Path to a ci-operator configuration file")
	flag.StringVar(&opt.fromDir, "from-dir", "", "Path to a directory with a directory structure holding ci-operator configuration files for multiple components")
	flag.BoolVar(&opt.fromReleaseRepo, "from-release-repo", false, "If set, it behaves like --from-dir=$GOPATH/src/github.com/openshift/release/ci-operator/config")

	flag.StringVar(&opt.toFile, "to-file", "", "Path to a Prow job configuration file where new jobs will be added. If the file does not exist, it will be created")
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

	if (o.toFile == "" && o.toDir == "") || (o.toFile != "" && o.toDir != "") {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{file,dir,release-repo}` options")
	}

	return nil
}

// Generate a PodSpec that runs `ci-operator`, to be used in Presubmit/Postsubmit
// Various pieces are derived from `org`, `repo`, `branch` and `target`.
// `additionalArgs` are passed as additional arguments to `ci-operator`
func generatePodSpec(org, repo, configFile, target string, additionalArgs ...string) *kubeapi.PodSpec {
	configMapKeyRef := kubeapi.EnvVarSource{
		ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
			LocalObjectReference: kubeapi.LocalObjectReference{
				Name: fmt.Sprintf("ci-operator-%s-%s", org, repo),
			},
			Key: configFile,
		},
	}

	return &kubeapi.PodSpec{
		ServiceAccountName: "ci-operator",
		Containers: []kubeapi.Container{
			{
				Image:   "ci-operator:latest",
				Command: []string{"ci-operator"},
				Args:    append([]string{"--artifact-dir=$(ARTIFACTS)", fmt.Sprintf("--target=%s", target)}, additionalArgs...),
				Env:     []kubeapi.EnvVar{{Name: "CONFIG_SPEC", ValueFrom: &configMapKeyRef}},
			},
		},
	}
}

type testDescription struct {
	Name   string
	Target string
}

// Generate a Presubmit job for the given parameters
func generatePresubmitForTest(test testDescription, org, repo, branch, configFilename string) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		Agent:        "kubernetes",
		AlwaysRun:    true,
		Brancher:     prowconfig.Brancher{Branches: []string{branch}},
		Context:      fmt.Sprintf("ci/prow/%s", test.Name),
		Name:         fmt.Sprintf("pull-ci-%s-%s-%s-%s", org, repo, branch, test.Name),
		RerunCommand: fmt.Sprintf("/test %s", test.Name),
		Spec:         generatePodSpec(org, repo, configFilename, test.Target),
		Trigger:      fmt.Sprintf(`((?m)^/test( all| %s),?(\\s+|$))`, test.Name),
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
			Decorate:         true,
		},
	}
}

// Generate a Presubmit job for the given parameters
func generatePostsubmitForTest(test testDescription, org, repo, branch, configFilename string, labels map[string]string, additionalArgs ...string) *prowconfig.Postsubmit {
	return &prowconfig.Postsubmit{
		Agent:    "kubernetes",
		Brancher: prowconfig.Brancher{Branches: []string{branch}},
		Name:     fmt.Sprintf("branch-ci-%s-%s-%s-%s", org, repo, branch, test.Name),
		Spec:     generatePodSpec(org, repo, configFilename, test.Target, additionalArgs...),
		Labels:   labels,
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: &prowkube.DecorationConfig{SkipCloning: true},
			Decorate:         true,
		},
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

// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additinal
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
func generateJobs(
	configSpec *cioperatorapi.ReleaseBuildConfiguration,
	org, repo, branch, configFilename string,
) *prowconfig.JobConfig {

	orgrepo := fmt.Sprintf("%s/%s", org, repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}

	for _, element := range configSpec.Tests {
		test := testDescription{Name: element.As, Target: element.As}
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(test, org, repo, branch, configFilename))
	}

	if len(configSpec.Images) > 0 {
		test := testDescription{Name: "images", Target: "[images]"}

		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(test, org, repo, branch, configFilename))

		// If the images are promoted to 'openshift' namespace, we may want to add
		// 'artifacts: images' label to the [images] postsubmit.
		labels := map[string]string{}
		if extractPromotionNamespace(configSpec) == "openshift" {
			labels["artifacts"] = "images"
		}
		imagesPostsubmit := generatePostsubmitForTest(test, org, repo, branch, configFilename, labels, "--promote")
		postsubmits[orgrepo] = append(postsubmits[orgrepo], *imagesPostsubmit)
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

	return configSpec, nil
}

// We use the directory/file naming convention to encode useful information
// about component repository information.
// The convention for ci-operator config files in this repo:
// ci-operator/config/ORGANIZATION/COMPONENT/BRANCH.yaml
func extractRepoElementsFromPath(configFilePath string) (string, string, string, string, error) {
	configSpecDir := filepath.Dir(configFilePath)
	repo := filepath.Base(configSpecDir)
	if repo == "." || repo == "/" {
		return "", "", "", "", fmt.Errorf("Could not extract repo from '%s' (expected path like '.../ORG/REPO/BRANCH.yaml", configFilePath)
	}

	org := filepath.Base(filepath.Dir(configSpecDir))
	if org == "." || org == "/" {
		return "", "", "", "", fmt.Errorf("Could not extract org from '%s' (expected path like '.../ORG/REPO/BRANCH.yaml", configFilePath)
	}

	fileName := filepath.Base(configFilePath)

	branch := strings.TrimSuffix(fileName, filepath.Ext(configFilePath))

	return org, repo, branch, fileName, nil
}

func generateProwJobsFromConfigFile(configFilePath string) (*prowconfig.JobConfig, string, string, error) {
	configSpec, err := readCiOperatorConfig(configFilePath)
	if err != nil {
		return nil, "", "", err
	}

	org, repo, branch, configFilename, err := extractRepoElementsFromPath(configFilePath)
	if err != nil {
		return nil, "", "", err
	}
	jobConfig := generateJobs(configSpec, org, repo, branch, configFilename)

	return jobConfig, org, repo, nil
}

// Given a JobConfig and a target directory, write the Prow job configuration
// into files in that directory. Presubmits and postsubmit jobs are written
// into separate files. If target files already exist and contain Prow job
// configuration, the jobs will be merged.
func writeJobsIntoComponentDirectory(jobDir, org, repo string, jobConfig *prowconfig.JobConfig) error {
	jobDirForComponent := filepath.Join(jobDir, org, repo)
	os.MkdirAll(jobDirForComponent, os.ModePerm)
	presubmitPath := filepath.Join(jobDirForComponent, fmt.Sprintf("%s-%s-presubmits.yaml", org, repo))
	postsubmitPath := filepath.Join(jobDirForComponent, fmt.Sprintf("%s-%s-postsubmits.yaml", org, repo))

	presubmits := *jobConfig
	presubmits.Postsubmits = nil
	postsubmits := *jobConfig
	postsubmits.Presubmits = nil

	if err := mergeJobsIntoFile(presubmitPath, &presubmits); err != nil {
		return err
	}

	if err := mergeJobsIntoFile(postsubmitPath, &postsubmits); err != nil {
		return err
	}

	return nil
}

// Iterate over all ci-operator config files under a given path and generate a
// Prow job configuration files for each one under a different path, mimicking
// the directory structure.
func generateJobsFromDirectory(configDir, jobDir, jobFile string) error {
	err := filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encontered while generating Prow job config: %v\n", err)
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".yaml" {
			jobConfig, org, repo, err := generateProwJobsFromConfigFile(path)
			if err != nil {
				return err
			}

			if len(jobDir) > 0 {
				if err = writeJobsIntoComponentDirectory(jobDir, org, repo, jobConfig); err != nil {
					return err
				}
			} else if len(jobFile) > 0 {
				if err = mergeJobsIntoFile(jobFile, jobConfig); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to generate all Prow jobs (%v)", err)
	}

	return nil
}

// Given two JobConfig, merge jobs from the `source` one to to `destination`
// one. Jobs are matched by name. All jobs from `source` will be present in
// `destination` - if there were jobs with the same name in `destination`, they
// will be overwritten. All jobs in `destination` that are not overwritten this
// way stay untouched.
func mergeJobConfig(destination, source *prowconfig.JobConfig) {
	// We do the same thing for both Presubmits and Postsubmits
	if source.Presubmits != nil {
		if destination.Presubmits == nil {
			destination.Presubmits = map[string][]prowconfig.Presubmit{}
		}
		for repo, jobs := range source.Presubmits {
			oldPresubmits, _ := destination.Presubmits[repo]
			destination.Presubmits[repo] = []prowconfig.Presubmit{}
			newJobs := map[string]prowconfig.Presubmit{}
			for _, job := range jobs {
				newJobs[job.Name] = job
			}
			for _, newJob := range source.Presubmits[repo] {
				destination.Presubmits[repo] = append(destination.Presubmits[repo], newJob)
			}

			for _, oldJob := range oldPresubmits {
				if _, hasKey := newJobs[oldJob.Name]; !hasKey {
					destination.Presubmits[repo] = append(destination.Presubmits[repo], oldJob)
				}
			}
		}
	}
	if source.Postsubmits != nil {
		if destination.Postsubmits == nil {
			destination.Postsubmits = map[string][]prowconfig.Postsubmit{}
		}
		for repo, jobs := range source.Postsubmits {
			oldPostsubmits, _ := destination.Postsubmits[repo]
			destination.Postsubmits[repo] = []prowconfig.Postsubmit{}
			newJobs := map[string]prowconfig.Postsubmit{}
			for _, job := range jobs {
				newJobs[job.Name] = job
			}
			for _, newJob := range source.Postsubmits[repo] {
				destination.Postsubmits[repo] = append(destination.Postsubmits[repo], newJob)
			}

			for _, oldJob := range oldPostsubmits {
				if _, hasKey := newJobs[oldJob.Name]; !hasKey {
					destination.Postsubmits[repo] = append(destination.Postsubmits[repo], oldJob)
				}
			}
		}
	}
}

// Given a JobConfig and a file path, write YAML representation of the config
// to the file path. If the file already contains some jobs, new ones will be
// merged with the existing ones.
func mergeJobsIntoFile(prowConfigPath string, jobConfig *prowconfig.JobConfig) error {
	existingJobConfig, err := jc.ReadFromFile(prowConfigPath)
	if err != nil {
		existingJobConfig = &prowconfig.JobConfig{}
	}

	mergeJobConfig(existingJobConfig, jobConfig)

	if err = jc.WriteToFile(prowConfigPath, existingJobConfig); err != nil {
		return err
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

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	flagSet.Parse(os.Args[1:])

	if opt.help {
		flagSet.Usage()
		os.Exit(0)
	}

	if err := opt.process(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(opt.fromFile) > 0 {
		jobConfig, org, repo, err := generateProwJobsFromConfigFile(opt.fromFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate jobs from '%s' (%v)\n", opt.fromFile, err)
			os.Exit(1)
		}
		if len(opt.toFile) > 0 { // from file to file
			if err := mergeJobsIntoFile(opt.toFile, jobConfig); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write jobs to '%s' (%v)\n", opt.toFile, err)
				os.Exit(1)
			}
		} else { // from file to directory
			if err := writeJobsIntoComponentDirectory(opt.toDir, org, repo, jobConfig); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write jobs to '%s' (%v)\n", opt.toDir, err)
				os.Exit(1)
			}
		}
	} else { // from directory
		if err := generateJobsFromDirectory(opt.fromDir, opt.toDir, opt.toFile); err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate jobs from '%s' (%v)\n", opt.fromDir, err)
			os.Exit(1)
		}
	}
}
