package rehearse

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	"k8s.io/client-go/rest"
)

const LogRehearsalJob = "rehearsal-job"

type prowJobClientWithDry struct {
	pj.ProwJobInterface

	dry bool
}

// NewProwJobClient creates a ProwJob client with a dry run capability
func NewProwJobClient(clusterConfig *rest.Config, namespace string, dry bool) (pj.ProwJobInterface, error) {
	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}
	pjclient := pjcset.ProwV1().ProwJobs(namespace)

	return &prowJobClientWithDry{pjclient, dry}, nil
}

func (c *prowJobClientWithDry) Create(pj *pjapi.ProwJob) (*pjapi.ProwJob, error) {
	if c.dry {
		jobAsYAML, err := yaml.Marshal(pj)
		if err != nil {
			return nil, fmt.Errorf("Failed to marshal job to YAML: %v", err)
		}
		fmt.Printf("%s\n", jobAsYAML)
		return pj, nil
	}

	return c.ProwJobInterface.Create(pj)
}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, source)

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s/%s", repo, strings.TrimPrefix(source.Context, "ci/prow/"))

	if len(source.Spec.Containers) != 1 {
		return nil, fmt.Errorf("Cannot rehearse jobs with more than 1 container in Spec")
	}
	container := source.Spec.Containers[0]

	if len(container.Command) != 1 || container.Command[0] != "ci-operator" {
		return nil, fmt.Errorf("Cannot rehearse jobs that have Command different from simple 'ci-operator'")
	}

	for _, arg := range container.Args {
		if strings.HasPrefix(arg, "--git-ref") || strings.HasPrefix(arg, "-git-ref") {
			return nil, fmt.Errorf("Cannot rehearse jobs that call ci-operator with '--git-ref' arg")
		}
	}

	if len(source.Branches) != 1 {
		return nil, fmt.Errorf("Cannot rehearse jobs that run over multiple branches")
	}
	branch := strings.TrimPrefix(strings.TrimSuffix(source.Branches[0], "$"), "^")

	gitrefArg := fmt.Sprintf("--git-ref=%s@%s", repo, branch)
	rehearsal.Spec.Containers[0].Args = append(source.Spec.Containers[0].Args, gitrefArg)

	return &rehearsal, nil
}

func submitRehearsal(job *prowconfig.Presubmit, refs *pjapi.Refs, logger logrus.FieldLogger, pjclient pj.ProwJobInterface) (*pjapi.ProwJob, error) {
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}

	pj := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *refs), labels)
	logger.WithFields(pjutil.ProwJobFields(&pj)).Info("Submitting a new prowjob.")

	return pjclient.Create(&pj)
}

// ExecuteJobs takes configs for a set of jobs which should be "rehearsed", and
// creates the ProwJobs that perform the actual rehearsal. *Rehearsal* means
// a "trial" execution of a Prow job configuration when the *job config* config
// is changed, giving feedback to Prow config authors on how the changes of the
// config would affect the "production" Prow jobs run on the actual target repos
func ExecuteJobs(toBeRehearsed map[string][]prowconfig.Presubmit, prNumber int, prRepo string, refs *pjapi.Refs, logger logrus.FieldLogger, pjclient pj.ProwJobInterface) error {
	rehearsals := []*prowconfig.Presubmit{}

	for repo, jobs := range toBeRehearsed {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, repo, prNumber)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
				continue
			}

			rehearsal, err = inlineCiOpConfig(rehearsal, repo, prRepo, jobLogger)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal job")
				continue
			}

			jobLogger.WithField(LogRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}

	if len(rehearsals) > 0 {
		for _, job := range rehearsals {
			created, err := submitRehearsal(job, refs, logger, pjclient)
			if err != nil {
				logger.WithError(err).Warn("Failed to execute a rehearsal presubmit")
			} else {
				logger.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
			}
		}
	} else {
		logger.Warn("No job rehearsals")
	}

	return nil
}

// Rehearsed Prow jobs may depend on ConfigMaps with content also modified by
// the tested PR. All ci-operator-based jobs use the `ci-operator-configs`
// ConfigMap that contains ci-operator configuration files. Rehearsed jobs
// need to have the PR-version of these files available. The following code
// takes care of creating a short-lived, rehearsal ConfigMap. The keys needed
// to be present are extracted from the rehearsal jobs and the rehearsal jobs
// are modified to use this ConfigMap instead of the "production" one.

const ciOperatorConfigsCMName = "ci-operator-configs"
const ciopConfigsInRepo = "ci-operator/config"

const LogCiopConfigFile = "ciop-config-file"
const LogCiopConfigRepo = "ciop-config-repo"

// If a job uses the `ci-operator-config` ConfigMap
func inlineCiOpConfig(job *prowconfig.Presubmit, targetRepo, prRepo string, logger logrus.FieldLogger) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, job)
	for _, container := range rehearsal.Spec.Containers {
		for index := range container.Env {
			env := &(container.Env[index])
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef.Name == ciOperatorConfigsCMName {
				filename := env.ValueFrom.ConfigMapKeyRef.Key
				fullPath := filepath.Join(prRepo, ciopConfigsInRepo, targetRepo, filename)

				logFields := logrus.Fields{LogCiopConfigFile: filename, LogCiopConfigRepo: targetRepo, LogRehearsalJob: job.Name}
				logger.WithFields(logFields).Info("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")

				ciOpConfigContent, err := ioutil.ReadFile(fullPath)

				if err != nil {
					logger.WithError(err).Warn("Failed to read ci-operator config file")
					return nil, err
				}

				env.Value = string(ciOpConfigContent)
				env.ValueFrom = nil
			}
		}
	}

	return &rehearsal, nil
}
