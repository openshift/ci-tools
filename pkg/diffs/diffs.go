package diffs

import (
	"fmt"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/sets"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	LogRepo       = "repo"
	LogJobName    = "job-name"
	LogReasons    = "reasons"
	logCiopConfig = "ciop-config"

	ChosenJob            = "Job has been chosen for rehearsal"
	newCiopConfigMsg     = "New ci-operator config file"
	changedCiopConfigMsg = "ci-operator config file changed"
)

// GetChangedCiopConfigs identifies CI Operator configurations that are new or have changed and
// determines for each which jobs are impacted if job-specific changes were made
func GetChangedCiopConfigs(masterConfig, prConfig config.DataByFilename, logger *logrus.Entry) (config.DataByFilename, map[string]sets.String) {
	ret := config.DataByFilename{}
	affectedJobs := map[string]sets.String{}

	for filename, newConfig := range prConfig {
		oldConfig, ok := masterConfig[filename]
		jobs := sets.NewString()

		// new ciop config
		if !ok {
			ret[filename] = newConfig
			logger.WithField(logCiopConfig, filename).Info(newCiopConfigMsg)
			continue
		}

		withoutTests := func(in cioperatorapi.ReleaseBuildConfiguration) cioperatorapi.ReleaseBuildConfiguration {
			var out cioperatorapi.ReleaseBuildConfiguration
			if err := deepcopy.Copy(&out, &in); err != nil {
				logrus.WithError(err).Warn("Could not deep copy configuration.") // this is a programming error
				return out
			}
			out.Tests = nil
			return out
		}

		if !equality.Semantic.DeepEqual(withoutTests(oldConfig.Configuration), withoutTests(newConfig.Configuration)) {
			logger.WithField(logCiopConfig, filename).Info(changedCiopConfigMsg)
			ret[filename] = newConfig
			continue
		}

		oldTests := getTestsByName(oldConfig.Configuration.Tests)
		newTests := getTestsByName(newConfig.Configuration.Tests)

		for as, test := range newTests {
			if !equality.Semantic.DeepEqual(oldTests[as], test) {
				logger.WithField(logCiopConfig, filename).Info(changedCiopConfigMsg)
				ret[filename] = newConfig
				jobs.Insert(as)
			}
		}

		if len(jobs) > 0 {
			affectedJobs[filename] = jobs
		}
	}
	return ret, affectedJobs
}

// GetChangedPresubmits returns a mapping of repo to presubmits to execute.
func GetChangedPresubmits(prowMasterConfig, prowPRConfig *prowconfig.Config, logger *logrus.Entry) config.Presubmits {
	ret := config.Presubmits{}

	masterJobs := getJobsByRepoAndName(prowMasterConfig.JobConfig.PresubmitsStatic)
	for repo, jobs := range prowPRConfig.JobConfig.PresubmitsStatic {
		for _, job := range jobs {
			var reasons []string
			master, existed := masterJobs[repo][job.Name]
			if existed {
				reasons = rehearsableDifferences(master.JobBase, job.JobBase)
				if master.Optional && !job.Optional {
					reasons = append(reasons, "changed to non-optional")
				}
				if !master.AlwaysRun && job.AlwaysRun {
					reasons = append(reasons, "changed to always run")
				}
			} else {
				reasons = []string{"new presubmit"}
			}

			if len(reasons) > 0 {
				selectionFields := logrus.Fields{LogRepo: repo, LogJobName: job.Name, LogReasons: strings.Join(reasons, ",")}
				logger.WithFields(selectionFields).Info(ChosenJob)
				ret.Add(repo, job, config.ChangedPresubmit)
			}
		}
	}
	return ret
}

// To compare two maps of slices, instead of iterating through the slice
// and compare the same key and index of the other map of slices,
// we convert them as `repo-> jobName-> Presubmit` to be able to
// access any specific elements of the Presubmits without the need to iterate in slices.
func getJobsByRepoAndName(presubmits config.Presubmits) map[string]map[string]prowconfig.Presubmit {
	jobsByRepo := make(map[string]map[string]prowconfig.Presubmit)

	for repo, preSubmitList := range presubmits {
		pm := make(map[string]prowconfig.Presubmit)
		for _, p := range preSubmitList {
			pm[p.Name] = p
		}
		jobsByRepo[repo] = pm
	}
	return jobsByRepo
}

// PostsubmitInContext is a postsubmit with the org/repo#branch for which it will trigger
type PostsubmitInContext struct {
	Metadata cioperatorapi.Metadata
	Job      prowconfig.Postsubmit
}

// GetImagesPostsubmitsForCiopConfigs determines the [images] postsubmit jobs affected by the changed
// ci-operator configurations
func GetImagesPostsubmitsForCiopConfigs(prowConfig *prowconfig.Config, ciopConfigs config.DataByFilename) []PostsubmitInContext {
	var ret []PostsubmitInContext

	for _, data := range ciopConfigs {
		jobNamePrefix := data.Info.JobName(jobconfig.PostsubmitPrefix, "")
		for _, job := range prowConfig.JobConfig.PostsubmitsStatic[fmt.Sprintf("%s/%s", data.Info.Org, data.Info.Repo)] {
			if job.Agent != string(pjapi.KubernetesAgent) {
				continue
			}
			if !strings.HasPrefix(job.Name, jobNamePrefix) {
				continue
			}
			testName := strings.TrimPrefix(job.Name, jobNamePrefix)

			if testName == "images" {
				ret = append(ret, PostsubmitInContext{
					Metadata: data.Info.Metadata,
					Job:      job,
				})
			}
		}
	}

	return ret
}

func GetPresubmitsForCiopConfigs(prowConfig *prowconfig.Config, ciopConfigs config.DataByFilename, affectedJobs map[string]sets.String, logger *logrus.Entry) config.Presubmits {
	ret := config.Presubmits{}

	for _, data := range ciopConfigs {
		orgRepo := fmt.Sprintf("%s/%s", data.Info.Org, data.Info.Repo)
		jobNamePrefix := data.Info.JobName(jobconfig.PresubmitPrefix, "")
		for _, job := range prowConfig.JobConfig.PresubmitsStatic[orgRepo] {
			if job.Agent != string(pjapi.KubernetesAgent) {
				continue
			}
			if !strings.HasPrefix(job.Name, jobNamePrefix) {
				continue
			}
			testName := strings.TrimPrefix(job.Name, jobNamePrefix)

			affectedJob, ok := affectedJobs[data.Info.Basename()]
			if ok && !affectedJob.Has(testName) {
				continue
			}

			selectionFields := logrus.Fields{LogRepo: orgRepo, LogJobName: job.Name, LogReasons: "ci-operator config changed"}
			logger.WithFields(selectionFields).Info(ChosenJob)
			ret.Add(orgRepo, job, config.ChangedCiopConfig)
		}
	}

	return ret
}

func getTestsByName(tests []cioperatorapi.TestStepConfiguration) map[string]cioperatorapi.TestStepConfiguration {
	ret := make(map[string]cioperatorapi.TestStepConfiguration)
	for _, test := range tests {
		ret[test.As] = test
	}
	return ret
}

// GetPresubmitsForClusterProfiles returns a filtered list of jobs from the
// Prow configuration, with only presubmits that use certain cluster profiles.
func GetPresubmitsForClusterProfiles(prowConfig *prowconfig.Config, profiles sets.String, logger *logrus.Entry) config.Presubmits {
	matches := func(job *prowconfig.Presubmit) bool {
		if job.Agent != string(pjapi.KubernetesAgent) {
			return false
		}
		for _, v := range job.Spec.Volumes {
			if v.Name != "cluster-profile" || v.Projected == nil {
				continue
			}
			for _, s := range v.Projected.Sources {
				if s.ConfigMap != nil && profiles.Has(s.ConfigMap.Name) {
					return true
				}
			}
		}
		return false
	}
	ret := config.Presubmits{}
	for repo, jobs := range prowConfig.JobConfig.PresubmitsStatic {
		for _, job := range jobs {
			if matches(&job) {
				selectionFields := logrus.Fields{LogRepo: repo, LogJobName: job.Name, LogReasons: "cluster profile changed"}
				logger.WithFields(selectionFields).Info(ChosenJob)
				ret.Add(repo, job, config.ChangedClusterProfile)
			}
		}
	}
	return ret
}

func rehearsableDifferences(master, pr prowconfig.JobBase) []string {
	var reasons []string
	if pr.Agent == string(pjapi.KubernetesAgent) {
		if master.Agent != pr.Agent {
			reasons = append(reasons, "agent changed")
		}
		if !equality.Semantic.DeepEqual(master.Spec, pr.Spec) {
			reasons = append(reasons, "spec changed")
		}
		if master.Cluster != pr.Cluster {
			reasons = append(reasons, "cluster changed")
		}
	}

	return reasons
}

// GetChangedPeriodics compares the periodic jobs from two prow configs and returns a list the changed periodics.
func GetChangedPeriodics(prowMasterConfig, prowPRConfig *prowconfig.Config, logger *logrus.Entry) config.Periodics {
	changed := config.Periodics{}
	masterByName := getPeriodicsPerName(prowMasterConfig.JobConfig.AllPeriodics())

	for name, job := range getPeriodicsPerName(prowPRConfig.JobConfig.AllPeriodics()) {
		var reasons []string
		master, existed := masterByName[name]
		if existed {
			reasons = rehearsableDifferences(master.JobBase, job.JobBase)
		} else {
			reasons = []string{"new periodic"}
		}
		if len(reasons) > 0 {
			selectionFields := logrus.Fields{LogJobName: name, LogReasons: reasons}
			logger.WithFields(selectionFields).Info(ChosenJob)
			changed.Add(job, config.ChangedPeriodic)
		}
	}

	return changed
}

func getPeriodicsPerName(periodics []prowconfig.Periodic) map[string]prowconfig.Periodic {
	ret := make(map[string]prowconfig.Periodic, len(periodics))
	for _, periodic := range periodics {
		ret[periodic.Name] = periodic
	}
	return ret
}
