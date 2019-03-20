package diffs

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/equality"
	utildiff "k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-operator/pkg/api"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
)

const (
	logRepo       = "repo"
	logJobName    = "job-name"
	logDiffs      = "diffs"
	logCiopConfig = "ciop-config"

	// ConfigInRepoPath is the prow config path from release repo
	ConfigInRepoPath = "cluster/ci/config/prow/config.yaml"
	// PluginsInRepoPath is the prow plugins config path from release repo
	PluginsInRepoPath = "cluster/ci/config/prow/plugins.yaml"
	// JobConfigInRepoPath is the prowjobs path from release repo
	JobConfigInRepoPath = "ci-operator/jobs"
	// CIOperatorConfigInRepoPath is the ci-operator config path from release repo
	CIOperatorConfigInRepoPath = "ci-operator/config"

	objectSpec  = ".Spec"
	objectAgent = ".Agent"

	chosenJob            = "Job has been chosen for rehearsal"
	newCiopConfigMsg     = "New ci-operator config file"
	changedCiopConfigMsg = "ci-operator config file changed"
)

func GetChangedCiopConfigs(masterConfig, prConfig config.CompoundCiopConfig, logger *logrus.Entry) (config.CompoundCiopConfig, map[string]sets.String) {
	ret := config.CompoundCiopConfig{}
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

		withoutTestsOldConfig := *masterConfig[filename]
		withoutTestsOldConfig.Tests = nil
		withoutTestsNewConfig := *prConfig[filename]
		withoutTestsNewConfig.Tests = nil

		if !equality.Semantic.DeepEqual(withoutTestsOldConfig, withoutTestsNewConfig) {
			logger.WithField(logCiopConfig, filename).Info(changedCiopConfigMsg)
			ret[filename] = newConfig
			continue
		}

		oldTests := getTestsByName(oldConfig.Tests)
		newTests := getTestsByName(newConfig.Tests)

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

	masterJobs := getJobsByRepoAndName(prowMasterConfig.JobConfig.Presubmits)
	for repo, jobs := range prowPRConfig.JobConfig.Presubmits {
		for _, job := range jobs {
			masterJob := masterJobs[repo][job.Name]
			logFields := logrus.Fields{logRepo: repo, logJobName: job.Name}

			if job.Agent == string(pjapi.KubernetesAgent) {
				// If the agent was changed and is a kubernetes agent, just choose the job for rehearse.
				if masterJob.Agent != job.Agent {
					logFields[logDiffs] = convertToReadableDiff(masterJob.Agent, job.Agent, objectAgent)
					logger.WithFields(logFields).Info(chosenJob)
					ret.Add(repo, job)
					continue
				}

				if !equality.Semantic.DeepEqual(masterJob.Spec, job.Spec) {
					logFields[logDiffs] = convertToReadableDiff(masterJob.Spec, job.Spec, objectSpec)
					logger.WithFields(logFields).Info(chosenJob)
					ret.Add(repo, job)
				}
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

// Converts the multiline diff string, to one line human readable that
// includes information about the object.
// Example:
//
// object[0].Args[0]:
//   a: "--artifact-dir=$(ARTIFACTS)"
//   b: "--artifact-dir=$(TEST_ARTIFACTS)"
//
// 	converted to:
//
//  .Spec.Containers[0].Args[0]:   a: '--artifact-dir=$(ARTIFACTS)'   b: '--artifact-dir=$(TEST_ARTIFACTS)'
//
func convertToReadableDiff(a, b interface{}, objName string) string {
	d := utildiff.ObjectReflectDiff(a, b)
	d = strings.Replace(d, "\nobject", fmt.Sprintf(" %s", objName), -1)
	d = strings.Replace(d, "\n", " ", -1)
	d = strings.Replace(d, "\"", "'", -1)
	return d
}

// GetChangedTemplates returns a mapping of the changed templates to be rehearsed.
func GetChangedTemplates(masterTemplates, prTemplates config.CiTemplates, logger *logrus.Entry) config.CiTemplates {
	changedTemplates := make(config.CiTemplates)

	for name, template := range prTemplates {
		logFields := logrus.Fields{"template-name": name}

		// new template
		if _, ok := masterTemplates[name]; !ok {
			changedTemplates[name] = template
			logger.WithFields(logFields).Info("new template detected")
			continue
		}

		if !equality.Semantic.DeepEqual(masterTemplates[name], prTemplates[name]) {
			logger.WithFields(logFields).Info("changed template detected")
			changedTemplates[name] = template
		}
	}
	return changedTemplates
}

func GetPresubmitsForCiopConfigs(prowConfig *prowconfig.Config, ciopConfigs config.CompoundCiopConfig, logger *logrus.Entry, affectedJobs map[string]sets.String) config.Presubmits {
	ret := config.Presubmits{}

	for repo, jobs := range prowConfig.JobConfig.Presubmits {
		for _, job := range jobs {
			if job.Agent != string(pjapi.KubernetesAgent) {
				continue
			}
			for _, env := range job.Spec.Containers[0].Env {
				if env.ValueFrom == nil {
					continue
				}
				if env.ValueFrom.ConfigMapKeyRef == nil {
					continue
				}
				if config.IsCiopConfigCM(env.ValueFrom.ConfigMapKeyRef.Name) {
					if _, ok := ciopConfigs[env.ValueFrom.ConfigMapKeyRef.Key]; ok {

						s := strings.Split(job.Name, "-")
						testName := s[len(s)-1]

						affectedJob, ok := affectedJobs[env.ValueFrom.ConfigMapKeyRef.Key]
						if ok && !affectedJob.Has(testName) {
							continue
						}

						ret.Add(repo, job)
					}
				}
			}
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
