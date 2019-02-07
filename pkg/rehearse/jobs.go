package rehearse

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/testing"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	pjclientsetfake "k8s.io/test-infra/prow/client/clientset/versioned/fake"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
)

const (
	rehearseLabel = "ci.openshift.org/rehearse"
)

type Loggers struct {
	Job, Debug logrus.FieldLogger
}

// NewProwJobClient creates a ProwJob client with a dry run capability
func NewProwJobClient(clusterConfig *rest.Config, namespace string, dry bool) (pj.ProwJobInterface, error) {
	if dry {
		pjcset := pjclientsetfake.NewSimpleClientset()
		pjcset.Fake.PrependReactor("create", "prowjobs", func(action testing.Action) (bool, runtime.Object, error) {
			pj := action.(testing.CreateAction).GetObject().(*pjapi.ProwJob)
			jobAsYAML, err := yaml.Marshal(pj)
			if err != nil {
				return true, nil, fmt.Errorf("failed to marshal job to YAML: %v", err)
			}
			fmt.Printf("%s\n", jobAsYAML)
			return false, nil, nil
		})
		return pjcset.ProwV1().ProwJobs(namespace), nil
	}
	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}
	return pjcset.ProwV1().ProwJobs(namespace), nil
}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int) (*prowconfig.Presubmit, error) {
	if err := filterJob(source); err != nil {
		return nil, err
	}

	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, source)

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s/%s", repo, strings.TrimPrefix(source.Context, "ci/prow/"))
	if rehearsal.Labels == nil {
		rehearsal.Labels = make(map[string]string, 1)
	}
	rehearsal.Labels[rehearseLabel] = strconv.Itoa(prNumber)

	branch := strings.TrimPrefix(strings.TrimSuffix(source.Branches[0], "$"), "^")
	gitrefArg := fmt.Sprintf("--git-ref=%s@%s", repo, branch)
	rehearsal.Spec.Containers[0].Args = append(source.Spec.Containers[0].Args, gitrefArg)

	return &rehearsal, nil
}

func filterJob(source *prowconfig.Presubmit) error {
	// there will always be exactly one container.
	container := source.Spec.Containers[0]

	if len(container.Command) != 1 || container.Command[0] != "ci-operator" {
		return fmt.Errorf("cannot rehearse jobs that have Command different from simple 'ci-operator'")
	}

	for _, arg := range container.Args {
		if strings.HasPrefix(arg, "--git-ref") || strings.HasPrefix(arg, "-git-ref") {
			return fmt.Errorf("cannot rehearse jobs that call ci-operator with '--git-ref' arg")
		}
	}
	if len(source.Spec.Volumes) > 0 {
		return fmt.Errorf("cannot rehearse jobs that need additional volumes mounted")
	}

	if len(source.Branches) == 0 {
		return fmt.Errorf("cannot rehearse jobs with no branches")
	}

	if len(source.Branches) != 1 {
		return fmt.Errorf("cannot rehearse jobs that run over multiple branches")
	}
	return nil
}

type ciOperatorConfigs interface {
	Load(repo, configFile string) (string, error)
}

const ciopConfigsInRepo = "ci-operator/config"

type ciOperatorConfigLoader struct {
	base string
}

func (c *ciOperatorConfigLoader) Load(repo, configFile string) (string, error) {
	fullPath := filepath.Join(c.base, repo, configFile)
	content, err := ioutil.ReadFile(fullPath)
	return string(content), err
}

const ciOperatorConfigsCMName = "ci-operator-configs"

const LogCiopConfigFile = "ciop-config-file"
const LogCiopConfigRepo = "ciop-config-repo"

// inlineCiOpConfig detects whether a job needs a ci-operator config file
// provided by a `ci-operator-configs` ConfigMap and if yes, returns a copy
// of the job where a reference to this ConfigMap is replaced by the content
// of the needed config file passed to the job as a direct value. This needs
// to happen because the rehearsed Prow jobs may depend on these config files
// being also changed by the tested PR.
func inlineCiOpConfig(job *prowconfig.Presubmit, targetRepo string, ciopConfigs ciOperatorConfigs, loggers Loggers) (*prowconfig.Presubmit, error) {
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

				logFields := logrus.Fields{LogCiopConfigFile: filename, LogCiopConfigRepo: targetRepo, LogRehearsalJob: job.Name}
				loggers.Debug.WithFields(logFields).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")

				ciOpConfigContent, err := ciopConfigs.Load(targetRepo, filename)

				if err != nil {
					loggers.Job.WithError(err).Warn("Failed to read ci-operator config file")
					return nil, err
				}

				env.Value = ciOpConfigContent
				env.ValueFrom = nil
			}
		}
	}

	return &rehearsal, nil
}

const LogRehearsalJob = "rehearsal-job"

func configureRehearsalJobs(toBeRehearsed map[string][]prowconfig.Presubmit, prRepo string, prNumber int, loggers Loggers) []*prowconfig.Presubmit {
	rehearsals := []*prowconfig.Presubmit{}
	ciopConfigs := &ciOperatorConfigLoader{filepath.Join(prRepo, ciopConfigsInRepo)}

	for repo, jobs := range toBeRehearsed {
		for _, job := range jobs {
			jobLogger := loggers.Job.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, repo, prNumber)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
				continue
			}

			rehearsal, err = inlineCiOpConfig(rehearsal, repo, ciopConfigs, loggers)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal job")
				continue
			}

			jobLogger.WithField(LogRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}

	return rehearsals
}
