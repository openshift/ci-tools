package diffs

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/equality"
	utildiff "k8s.io/apimachinery/pkg/util/diff"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
)

const (
	// ConfigInRepoPath is the prow config path from release repo
	ConfigInRepoPath = "cluster/ci/config/prow/config.yaml"
	// JobConfigInRepoPath is the prowjobs path from release repo
	JobConfigInRepoPath = "ci-operator/jobs"
	// TemplatesPath is the path of the templates from release repo
	TemplatesPath = "ci-operator/templates"
	// CiopConfigInRepoPath is the ci-operator config path from release repo
	CiopConfigInRepoPath = "ci-operator/config"

	logRepo       = "repo"
	logJobName    = "job-name"
	logDiffs      = "diffs"
	logCiopConfig = "ciop-config"

	objectSpec  = ".Spec"
	objectAgent = ".Agent"

	chosenJob            = "Job has been chosen for rehearsal"
	newCiopConfigMsg     = "New ci-operator config file"
	changedCiopConfigMsg = "ci-operator config file changed"
)

func GetChangedCiopConfigs(masterConfig, prConfig config.CompoundCiopConfig, logger *logrus.Entry) config.CompoundCiopConfig {
	ret := config.CompoundCiopConfig{}

	for filename, newConfig := range prConfig {
		oldConfig, ok := masterConfig[filename]

		// new ciop config
		if !ok {
			ret[filename] = newConfig
			logger.WithField(logCiopConfig, filename).Info(newCiopConfigMsg)
			continue
		}

		if !equality.Semantic.DeepEqual(oldConfig, newConfig) {
			logger.WithField(logCiopConfig, filename).Info(changedCiopConfigMsg)
			ret[filename] = newConfig
		}
	}

	return ret
}

// GetChangedPresubmits returns a mapping of repo to presubmits to execute.
func GetChangedPresubmits(prowMasterConfig, prowPRConfig *prowconfig.Config, logger *logrus.Entry) map[string][]prowconfig.Presubmit {
	ret := make(map[string][]prowconfig.Presubmit)

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
					ret[repo] = append(ret[repo], job)
					continue
				}

				if !equality.Semantic.DeepEqual(masterJob.Spec, job.Spec) {
					logFields[logDiffs] = convertToReadableDiff(masterJob.Spec, job.Spec, objectSpec)
					logger.WithFields(logFields).Info(chosenJob)
					ret[repo] = append(ret[repo], job)
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
func getJobsByRepoAndName(presubmits map[string][]prowconfig.Presubmit) map[string]map[string]prowconfig.Presubmit {
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
func GetChangedTemplates(masterTemplates, prTemplates map[string]*templateapi.Template, logger *logrus.Entry) map[string]*templateapi.Template {
	changedTemplates := make(map[string]*templateapi.Template)

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
