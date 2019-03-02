package jobconfig

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

// DataWithInfo describes the metadata for a Prow job configuration file
type Info struct {
	Org    string
	Repo   string
	Branch string
	// Type is the type of ProwJob contained in this file
	Type string
	// Filename is the full path to the file on disk
	Filename string
}

// Basename returns the unique name for this file in the config
func (e *Info) Basename() string {
	return fmt.Sprintf("%s.yaml", strings.Join([]string{e.Org, e.Repo, e.Branch, e.Type}, "-"))
}

// We use the directory/file naming convention to encode useful information
// about component repository information.
// The convention for prow job config files in this repo:
// ci-operator/jobs/ORGANIZATION/COMPONENT/ORGANIZATION-COMPONENT-BRANCH-JOBTYPE.yaml
func extractInfoFromPath(configFilePath string) (*Info, error) {
	configSpecDir := filepath.Dir(configFilePath)
	repo := filepath.Base(configSpecDir)
	if repo == "." || repo == "/" {
		return nil, fmt.Errorf("could not extract repo from '%s'", configFilePath)
	}

	org := filepath.Base(filepath.Dir(configSpecDir))
	if org == "." || org == "/" {
		return nil, fmt.Errorf("could not extract org from '%s'", configFilePath)
	}

	// take org/repo/org-repo-branch-type.yaml and:
	// consider only the base name, then
	// remove .yaml extension, then
	// strip the "org-repo-" prefix, then
	// isolate the "-type" suffix, then
	// extract the branch
	basename := filepath.Base(configFilePath)
	basenameWithoutSuffix := strings.TrimSuffix(basename, filepath.Ext(configFilePath))
	orgRepo := fmt.Sprintf("%s-%s-", org, repo)
	if ! strings.HasPrefix(basenameWithoutSuffix, orgRepo) {
		return nil, fmt.Errorf("file name was not prefixed with %q: %q", orgRepo, basenameWithoutSuffix)
	}
	branchType := strings.TrimPrefix(basenameWithoutSuffix, orgRepo)
	typeIndex := strings.LastIndex(branchType, "-")
	var branch, jobType string
	if typeIndex == -1 {
		if branchType != "periodics" {
			return nil, fmt.Errorf("file name does not contain job type: %q", basenameWithoutSuffix)
		}
		branch = ""
		jobType = "periodics"
	} else {
		branch = branchType[:typeIndex]
		jobType = branchType[typeIndex+1:]
	}

	return &Info{
		Org:      org,
		Repo:     repo,
		Branch:   branch,
		Type:     jobType,
		Filename: configFilePath,
	}, nil
}

func OperateOnJobConfigDir(configDir string, callback func(*prowconfig.JobConfig, *Info) error) error {
	if err := filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		logger := logrus.WithField("source-file", path)
		if err != nil {
			logger.WithError(err).Error("Failed to walk file/directory")
			return nil
		}

		if !info.IsDir() && filepath.Ext(path) == ".yaml" {
			var configPart *prowconfig.JobConfig
			if configPart, err = readFromFile(path); err != nil {
				logger.WithError(err).Error("Failed to read Prow job config")
				return nil
			}

			info, err := extractInfoFromPath(path)
			if err != nil {
				logger.WithError(err).Warn("Failed to determine info for prow job config")
				return nil
			}

			return callback(configPart, info)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to operator on Prow job configs: %v", err)
	}
	return nil
}

// ReadFromDir reads Prow job config from a directory and merges into one config
func ReadFromDir(dir string) (*prowconfig.JobConfig, error) {
	jobConfig := &prowconfig.JobConfig{
		Presubmits:  map[string][]prowconfig.Presubmit{},
		Postsubmits: map[string][]prowconfig.Postsubmit{},
	}
	if err := OperateOnJobConfigDir(dir, func(config *prowconfig.JobConfig, elements *Info) error {
		mergeConfigs(jobConfig, config)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to load all Prow jobs: %v", err)
	}

	return jobConfig, nil
}

// mergeConfigs merges job configuration from part into dest
func mergeConfigs(dest, part *prowconfig.JobConfig) {
	if part.Presubmits != nil {
		if dest.Presubmits == nil {
			dest.Presubmits = map[string][]prowconfig.Presubmit{}
		}
		for repo := range part.Presubmits {
			if _, ok := dest.Presubmits[repo]; ok {
				dest.Presubmits[repo] = append(dest.Presubmits[repo], part.Presubmits[repo]...)
			} else {
				dest.Presubmits[repo] = part.Presubmits[repo]
			}
		}
	}
	if part.Postsubmits != nil {
		if dest.Postsubmits == nil {
			dest.Postsubmits = map[string][]prowconfig.Postsubmit{}
		}
		for repo := range part.Postsubmits {
			if _, ok := dest.Postsubmits[repo]; ok {
				dest.Postsubmits[repo] = append(dest.Postsubmits[repo], part.Postsubmits[repo]...)
			} else {
				dest.Postsubmits[repo] = part.Postsubmits[repo]
			}
		}
	}
}

// readFromFile reads Prow job config from a YAML file
func readFromFile(path string) (*prowconfig.JobConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Prow job config (%v)", err)
	}

	var jobConfig *prowconfig.JobConfig
	if err := yaml.Unmarshal(data, &jobConfig); err != nil {
		return nil, fmt.Errorf("failed to load Prow job config (%v)", err)
	}
	if jobConfig == nil { // happens when `data` is empty
		return nil, fmt.Errorf("failed to load Prow job config")
	}

	return jobConfig, nil
}

// Given a JobConfig and a target directory, write the Prow job configuration
// into files in that directory. Jobs are sharded by branch and by type. If
// target files already exist and contain Prow job configuration, the jobs will
// be merged.
func WriteToDir(jobDir, org, repo string, jobConfig *prowconfig.JobConfig) error {
	allJobs := sets.String{}
	files := map[string]*prowconfig.JobConfig{}
	key := fmt.Sprintf("%s/%s", org, repo)
	for _, job := range jobConfig.Presubmits[key] {
		allJobs.Insert(job.Name)
		branch := "master"
		if len(job.Branches) > 0 {
			branch = job.Branches[0]
			// branches may be regexps, strip regexp characters and trailing dashes / slashes
			branch = MakeRegexFilenameLabel(branch)
		}
		file := fmt.Sprintf("%s-%s-%s-presubmits.yaml", org, repo, branch)
		if _, ok := files[file]; ok {
			files[file].Presubmits[key] = append(files[file].Presubmits[key], job)
		} else {
			files[file] = &prowconfig.JobConfig{Presubmits: map[string][]prowconfig.Presubmit{
				key: {job},
			}}
		}
	}
	for _, job := range jobConfig.Postsubmits[key] {
		allJobs.Insert(job.Name)
		branch := "master"
		if len(job.Branches) > 0 {
			branch = job.Branches[0]
			// branches may be regexps, strip regexp characters and trailing dashes / slashes
			branch = MakeRegexFilenameLabel(branch)
		}
		file := fmt.Sprintf("%s-%s-%s-postsubmits.yaml", org, repo, branch)
		if _, ok := files[file]; ok {
			files[file].Postsubmits[key] = append(files[file].Postsubmits[key], job)
		} else {
			files[file] = &prowconfig.JobConfig{Postsubmits: map[string][]prowconfig.Postsubmit{
				key: {job},
			}}
		}
	}

	jobDirForComponent := filepath.Join(jobDir, org, repo)
	if err := os.MkdirAll(jobDirForComponent, os.ModePerm); err != nil {
		return err
	}
	for file := range files {
		if err := mergeJobsIntoFile(filepath.Join(jobDirForComponent, file), files[file], allJobs); err != nil {
			return err
		}
	}

	return nil
}

// Given a JobConfig and a file path, write YAML representation of the config
// to the file path. If the file already contains some jobs, new ones will be
// merged with the existing ones.
func mergeJobsIntoFile(prowConfigPath string, jobConfig *prowconfig.JobConfig, allJobs sets.String) error {
	existingJobConfig, err := readFromFile(prowConfigPath)
	if err != nil {
		existingJobConfig = &prowconfig.JobConfig{}
	}

	mergeJobConfig(existingJobConfig, jobConfig, allJobs)
	sortConfigFields(existingJobConfig)

	return writeToFile(prowConfigPath, existingJobConfig)
}

// Given two JobConfig, merge jobs from the `source` one to to `destination`
// one. Jobs are matched by name. All jobs from `source` will be present in
// `destination` - if there were jobs with the same name in `destination`, they
// will be updated. All jobs in `destination` that are not overwritten this
// way and are not otherwise in the set of all jobs being written stay untouched.
func mergeJobConfig(destination, source *prowconfig.JobConfig, allJobs sets.String) {
	// We do the same thing for both Presubmits and Postsubmits
	if source.Presubmits != nil {
		if destination.Presubmits == nil {
			destination.Presubmits = map[string][]prowconfig.Presubmit{}
		}
		for repo, jobs := range source.Presubmits {
			oldJobs := map[string]prowconfig.Presubmit{}
			newJobs := map[string]prowconfig.Presubmit{}
			for _, job := range destination.Presubmits[repo] {
				oldJobs[job.Name] = job
			}
			for _, job := range jobs {
				newJobs[job.Name] = job
			}

			var mergedJobs []prowconfig.Presubmit
			for newJobName := range newJobs {
				newJob := newJobs[newJobName]
				if oldJob, existed := oldJobs[newJobName]; existed {
					mergedJobs = append(mergedJobs, mergePresubmits(&oldJob, &newJob))
				} else {
					mergedJobs = append(mergedJobs, newJob)
				}
			}
			for oldJobName := range oldJobs {
				if _, updated := newJobs[oldJobName]; !updated && !allJobs.Has(oldJobName) {
					mergedJobs = append(mergedJobs, oldJobs[oldJobName])
				}
			}
			destination.Presubmits[repo] = mergedJobs
		}
	}
	if source.Postsubmits != nil {
		if destination.Postsubmits == nil {
			destination.Postsubmits = map[string][]prowconfig.Postsubmit{}
		}
		for repo, jobs := range source.Postsubmits {
			oldJobs := map[string]prowconfig.Postsubmit{}
			newJobs := map[string]prowconfig.Postsubmit{}
			for _, job := range destination.Postsubmits[repo] {
				oldJobs[job.Name] = job
			}
			for _, job := range jobs {
				newJobs[job.Name] = job
			}

			var mergedJobs []prowconfig.Postsubmit
			for newJobName := range newJobs {
				newJob := newJobs[newJobName]
				if oldJob, existed := oldJobs[newJobName]; existed {
					mergedJobs = append(mergedJobs, mergePostsubmits(&oldJob, &newJob))
				} else {
					mergedJobs = append(mergedJobs, newJob)
				}
			}
			for oldJobName := range oldJobs {
				if _, updated := newJobs[oldJobName]; !updated && !allJobs.Has(oldJobName) {
					mergedJobs = append(mergedJobs, oldJobs[oldJobName])
				}
			}
			destination.Postsubmits[repo] = mergedJobs
		}
	}
}

// mergePresubmits merges the two configurations, preferring fields
// in the new configuration unless the fields are set in the old
// configuration and cannot be derived from the ci-operator configuration
func mergePresubmits(old, new *prowconfig.Presubmit) prowconfig.Presubmit {
	merged := *new

	merged.AlwaysRun = old.AlwaysRun
	merged.RunIfChanged = old.RunIfChanged
	merged.Optional = old.Optional
	merged.MaxConcurrency = old.MaxConcurrency
	merged.SkipReport = old.SkipReport

	return merged
}

// mergePostsubmits merges the two configurations, preferring fields
// in the new configuration unless the fields are set in the old
// configuration and cannot be derived from the ci-operator configuration
func mergePostsubmits(old, new *prowconfig.Postsubmit) prowconfig.Postsubmit {
	merged := *new

	merged.MaxConcurrency = old.MaxConcurrency

	return merged
}

// sortConfigFields sorts array fields inside of job configurations so
// that their serialized form is stable and deterministic
func sortConfigFields(jobConfig *prowconfig.JobConfig) {
	for repo := range jobConfig.Presubmits {
		sort.Slice(jobConfig.Presubmits[repo], func(i, j int) bool {
			return jobConfig.Presubmits[repo][i].Name < jobConfig.Presubmits[repo][j].Name
		})
		for job := range jobConfig.Presubmits[repo] {
			if jobConfig.Presubmits[repo][job].Spec != nil {
				sortPodSpec(jobConfig.Presubmits[repo][job].Spec)
			}
		}
	}
	for repo := range jobConfig.Postsubmits {
		sort.Slice(jobConfig.Postsubmits[repo], func(i, j int) bool {
			return jobConfig.Postsubmits[repo][i].Name < jobConfig.Postsubmits[repo][j].Name
		})
		for job := range jobConfig.Postsubmits[repo] {
			if jobConfig.Postsubmits[repo][job].Spec != nil {
				sortPodSpec(jobConfig.Postsubmits[repo][job].Spec)
			}
		}
	}
}

func sortPodSpec(spec *v1.PodSpec) {
	if len(spec.Volumes) > 0 {
		sort.Slice(spec.Volumes, func(i, j int) bool {
			return spec.Volumes[i].Name < spec.Volumes[j].Name
		})
	}
	if len(spec.Containers) > 0 {
		sort.Slice(spec.Containers, func(i, j int) bool {
			return spec.Containers[i].Name < spec.Containers[j].Name
		})
		for container := range spec.Containers {
			if len(spec.Containers[container].VolumeMounts) > 0 {
				sort.Slice(spec.Containers[container].VolumeMounts, func(i, j int) bool {
					return spec.Containers[container].VolumeMounts[i].Name < spec.Containers[container].VolumeMounts[j].Name
				})
			}
			if len(spec.Containers[container].Command) == 1 && spec.Containers[container].Command[0] == "ci-operator" {
				if len(spec.Containers[container].Args) > 0 {
					sort.Strings(spec.Containers[container].Args)
				}
			}
			if len(spec.Containers[container].Env) > 0 {
				sort.Slice(spec.Containers[container].Env, func(i, j int) bool {
					return spec.Containers[container].Env[i].Name < spec.Containers[container].Env[j].Name
				})
			}
		}
	}
}

// writeToFile writes Prow job config to a YAML file
func writeToFile(path string, jobConfig *prowconfig.JobConfig) error {
	jobConfigAsYaml, err := yaml.Marshal(*jobConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal the job config (%v)", err)
	}
	if err := ioutil.WriteFile(path, jobConfigAsYaml, 0664); err != nil {
		return err
	}

	return nil
}

var regexParts = regexp.MustCompile(`[^\w\-\.]+`)

func MakeRegexFilenameLabel(possibleRegex string) string {
	label := regexParts.ReplaceAllString(possibleRegex, "")
	label = strings.TrimLeft(strings.TrimRight(label, "-._"), "-._")
	if len(label) == 0 {
		label = "master"
	}
	return label
}
