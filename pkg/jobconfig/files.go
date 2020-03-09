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
	"github.com/openshift/ci-tools/pkg/promotion"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
)

type ProwgenLabel string

const (
	ProwJobLabelGenerated              = "ci-operator.openshift.io/prowgen-controlled"
	CanBeRehearsedLabel                = "pj-rehearse.openshift.io/can-be-rehearsed"
	Generated             ProwgenLabel = "true"
	New                   ProwgenLabel = "newly-generated"
	PresubmitPrefix                    = "pull"
	PostsubmitPrefix                   = "branch"
	PeriodicPrefix                     = "periodic"
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
func (i *Info) Basename() string {
	parts := []string{i.Org, i.Repo, i.Branch, i.Type}
	if i.Type == "periodics" && i.Branch == "" {
		parts = []string{i.Org, i.Repo, i.Type}
	}
	return fmt.Sprintf("%s.yaml", strings.Join(parts, "-"))
}

// ConfigMapName returns the configmap in which we expect this file to be uploaded
func (i *Info) ConfigMapName() string {
	// put periodics not directly correlated to code in the misc job
	if i.Type == "periodics" && i.Branch == "" {
		return fmt.Sprintf("job-config-%s", promotion.FlavorForBranch(""))
	}
	return fmt.Sprintf("job-config-%s", promotion.FlavorForBranch(i.Branch))
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
	if !strings.HasPrefix(basenameWithoutSuffix, orgRepo) {
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
	return OperateOnJobConfigSubdir(configDir, "", callback)
}

func OperateOnJobConfigSubdir(configDir, subDir string, callback func(*prowconfig.JobConfig, *Info) error) error {
	if err := filepath.Walk(filepath.Join(configDir, subDir), func(path string, info os.FileInfo, err error) error {
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
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
		Periodics:         []prowconfig.Periodic{},
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
	if part.PresubmitsStatic != nil {
		if dest.PresubmitsStatic == nil {
			dest.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
		}
		for repo := range part.PresubmitsStatic {
			if _, ok := dest.PresubmitsStatic[repo]; ok {
				dest.PresubmitsStatic[repo] = append(dest.PresubmitsStatic[repo], part.PresubmitsStatic[repo]...)
			} else {
				dest.PresubmitsStatic[repo] = part.PresubmitsStatic[repo]
			}
		}
	}
	if part.PostsubmitsStatic != nil {
		if dest.PostsubmitsStatic == nil {
			dest.PostsubmitsStatic = map[string][]prowconfig.Postsubmit{}
		}
		for repo := range part.PostsubmitsStatic {
			if _, ok := dest.PostsubmitsStatic[repo]; ok {
				dest.PostsubmitsStatic[repo] = append(dest.PostsubmitsStatic[repo], part.PostsubmitsStatic[repo]...)
			} else {
				dest.PostsubmitsStatic[repo] = part.PostsubmitsStatic[repo]
			}
		}
	}
	dest.Periodics = append(dest.Periodics, part.Periodics...)
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
	for _, job := range jobConfig.PresubmitsStatic[key] {
		allJobs.Insert(job.Name)
		branch := "master"
		if len(job.Branches) > 0 {
			branch = job.Branches[0]
			// branches may be regexps, strip regexp characters and trailing dashes / slashes
			branch = MakeRegexFilenameLabel(branch)
		}
		file := fmt.Sprintf("%s-%s-%s-presubmits.yaml", org, repo, branch)
		if _, ok := files[file]; ok {
			files[file].PresubmitsStatic[key] = append(files[file].PresubmitsStatic[key], job)
		} else {
			files[file] = &prowconfig.JobConfig{PresubmitsStatic: map[string][]prowconfig.Presubmit{
				key: {job},
			}}
		}
	}
	for _, job := range jobConfig.PostsubmitsStatic[key] {
		allJobs.Insert(job.Name)
		branch := "master"
		if len(job.Branches) > 0 {
			branch = job.Branches[0]
			// branches may be regexps, strip regexp characters and trailing dashes / slashes
			branch = MakeRegexFilenameLabel(branch)
		}
		file := fmt.Sprintf("%s-%s-%s-postsubmits.yaml", org, repo, branch)
		if _, ok := files[file]; ok {
			files[file].PostsubmitsStatic[key] = append(files[file].PostsubmitsStatic[key], job)
		} else {
			files[file] = &prowconfig.JobConfig{PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
				key: {job},
			}}
		}
	}
	for _, job := range jobConfig.Periodics {
		if len(job.ExtraRefs) == 0 {
			continue
		}
		if job.ExtraRefs[0].Org != org || job.ExtraRefs[0].Repo != repo {
			continue
		}
		allJobs.Insert(job.Name)
		branch := MakeRegexFilenameLabel(job.ExtraRefs[0].BaseRef)
		file := fmt.Sprintf("%s-%s-%s-periodics.yaml", org, repo, branch)
		if _, ok := files[file]; ok {
			files[file].Periodics = append(files[file].Periodics, job)
		} else {
			files[file] = &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{job}}
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
// merged with the existing ones. The resulting job config file will contain
// the following:
// - All jobs *not* generated by Prowgen already present in the destination file
// - All jobs present in the source JobConfig, but not in the destination
// - All jobs present in the source JobConfig *and* in the destination will have
//   the source configuration, with the exception of several fields whose values
//   will be kept as present in the destination (see mergePre/Postsubmits methods)
//
// Note that jobs generated by Prowgen present in destination, but not in the
// source will not be included in the destination.
func mergeJobsIntoFile(prowConfigPath string, jobConfig *prowconfig.JobConfig, allJobs sets.String) error {
	existingJobConfig, err := readFromFile(prowConfigPath)
	if err != nil {
		existingJobConfig = &prowconfig.JobConfig{}
	}

	mergeJobConfig(existingJobConfig, jobConfig, allJobs)
	sortConfigFields(existingJobConfig)

	return WriteToFile(prowConfigPath, existingJobConfig)
}

// Given two JobConfig, merge jobs from the `source` one to to `destination`
// one. Jobs are matched by name. All jobs from `source` will be present in
// `destination` - if there were jobs with the same name in `destination`, they
// will be updated. All jobs in `destination` that are not overwritten this
// way and are not otherwise in the set of all jobs being written stay untouched.
func mergeJobConfig(destination, source *prowconfig.JobConfig, allJobs sets.String) {
	// We do the same thing for all jobs
	if source.PresubmitsStatic != nil {
		if destination.PresubmitsStatic == nil {
			destination.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
		}
		for repo, jobs := range source.PresubmitsStatic {
			oldJobs := map[string]prowconfig.Presubmit{}
			newJobs := map[string]prowconfig.Presubmit{}
			for _, job := range destination.PresubmitsStatic[repo] {
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
			destination.PresubmitsStatic[repo] = mergedJobs
		}
	}
	if source.PostsubmitsStatic != nil {
		if destination.PostsubmitsStatic == nil {
			destination.PostsubmitsStatic = map[string][]prowconfig.Postsubmit{}
		}
		for repo, jobs := range source.PostsubmitsStatic {
			oldJobs := map[string]prowconfig.Postsubmit{}
			newJobs := map[string]prowconfig.Postsubmit{}
			for _, job := range destination.PostsubmitsStatic[repo] {
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
			destination.PostsubmitsStatic[repo] = mergedJobs
		}
	}
	if len(source.Periodics) != 0 {
		if len(destination.Periodics) == 0 {
			destination.Periodics = []prowconfig.Periodic{}
		}
		oldJobs := map[string]prowconfig.Periodic{}
		newJobs := map[string]prowconfig.Periodic{}
		for _, job := range source.Periodics {
			newJobs[job.Name] = job
		}
		for _, job := range destination.Periodics {
			oldJobs[job.Name] = job
		}

		var mergedJobs []prowconfig.Periodic
		for newJobName := range newJobs {
			newJob := newJobs[newJobName]
			if oldJob, existed := oldJobs[newJobName]; existed {
				mergedJobs = append(mergedJobs, mergePeriodics(&oldJob, &newJob))
			} else {
				mergedJobs = append(mergedJobs, newJob)
			}
		}
		for oldJobName := range oldJobs {
			if _, updated := newJobs[oldJobName]; !updated && !allJobs.Has(oldJobName) {
				mergedJobs = append(mergedJobs, oldJobs[oldJobName])
			}
		}
		destination.Periodics = mergedJobs
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
	merged.Cluster = old.Cluster

	return merged
}

// mergePostsubmits merges the two configurations, preferring fields
// in the new configuration unless the fields are set in the old
// configuration and cannot be derived from the ci-operator configuration
func mergePostsubmits(old, new *prowconfig.Postsubmit) prowconfig.Postsubmit {
	merged := *new

	merged.MaxConcurrency = old.MaxConcurrency
	merged.Cluster = old.Cluster

	return merged
}

// mergePeriodics merges the two configurations, preferring fields
// in the new configuration unless the fields are set in the old
// configuration and cannot be derived from the ci-operator configuration
func mergePeriodics(old, new *prowconfig.Periodic) prowconfig.Periodic {
	merged := *new

	merged.MaxConcurrency = old.MaxConcurrency
	merged.Cluster = old.Cluster

	return merged
}

// sortConfigFields sorts array fields inside of job configurations so
// that their serialized form is stable and deterministic
func sortConfigFields(jobConfig *prowconfig.JobConfig) {
	for repo := range jobConfig.PresubmitsStatic {
		sort.Slice(jobConfig.PresubmitsStatic[repo], func(i, j int) bool {
			return jobConfig.PresubmitsStatic[repo][i].Name < jobConfig.PresubmitsStatic[repo][j].Name
		})
		for job := range jobConfig.PresubmitsStatic[repo] {
			if jobConfig.PresubmitsStatic[repo][job].Spec != nil {
				sortPodSpec(jobConfig.PresubmitsStatic[repo][job].Spec)
			}
		}
	}
	for repo := range jobConfig.PostsubmitsStatic {
		sort.Slice(jobConfig.PostsubmitsStatic[repo], func(i, j int) bool {
			return jobConfig.PostsubmitsStatic[repo][i].Name < jobConfig.PostsubmitsStatic[repo][j].Name
		})
		for job := range jobConfig.PostsubmitsStatic[repo] {
			if jobConfig.PostsubmitsStatic[repo][job].Spec != nil {
				sortPodSpec(jobConfig.PostsubmitsStatic[repo][job].Spec)
			}
		}
	}

	sort.Slice(jobConfig.Periodics, func(i, j int) bool {
		return jobConfig.Periodics[i].Name < jobConfig.Periodics[j].Name
	})
	for job := range jobConfig.Periodics {
		if jobConfig.Periodics[job].Spec != nil {
			sortPodSpec(jobConfig.Periodics[job].Spec)
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

// WriteToFile writes Prow job config to a YAML file
func WriteToFile(path string, jobConfig *prowconfig.JobConfig) error {
	jobConfigAsYaml, err := yaml.Marshal(*jobConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal the job config (%v)", err)
	}
	if err := ioutil.WriteFile(path, jobConfigAsYaml, 0664); err != nil {
		return err
	}

	return nil
}

var regexParts = regexp.MustCompile(`[^\w\-.]+`)

func MakeRegexFilenameLabel(possibleRegex string) string {
	label := regexParts.ReplaceAllString(possibleRegex, "")
	label = strings.TrimLeft(strings.TrimRight(label, "-._"), "-._")
	if len(label) == 0 {
		label = "master"
	}
	return label
}
