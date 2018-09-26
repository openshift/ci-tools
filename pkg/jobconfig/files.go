package jobconfig

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	"github.com/ghodss/yaml"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

// ReadFromDir reads Prow job config from a directory and merges into one config
func ReadFromDir(dir string) (*prowconfig.JobConfig, error) {
	jobConfig := &prowconfig.JobConfig{
		Presubmits:  map[string][]prowconfig.Presubmit{},
		Postsubmits: map[string][]prowconfig.Postsubmit{},
	}
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to walk file/directory '%s'", path)
			return nil
		}

		if !info.IsDir() && filepath.Ext(path) == ".yaml" {
			var configPart *prowconfig.JobConfig
			if configPart, err = readFromFile(path); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read Prow job config from '%s' (%v)", path, err)
				return nil
			}

			mergeConfigs(jobConfig, configPart)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to determinize all Prow jobs: %v", err)
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
	for repo := range existingJobConfig.Presubmits {
		sort.Slice(existingJobConfig.Presubmits[repo], func(i, j int) bool {
			return existingJobConfig.Presubmits[repo][i].Name < existingJobConfig.Presubmits[repo][j].Name
		})
	}
	for repo := range existingJobConfig.Postsubmits {
		sort.Slice(existingJobConfig.Postsubmits[repo], func(i, j int) bool {
			return existingJobConfig.Postsubmits[repo][i].Name < existingJobConfig.Postsubmits[repo][j].Name
		})
	}

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
