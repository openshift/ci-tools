package rehearse

import (
	"fmt"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"

	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"
)

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

func submitRehearsal(job *prowconfig.Presubmit, refs *pjapi.Refs, logger logrus.FieldLogger, pjclient pj.ProwJobInterface, dry bool) (*pjapi.ProwJob, error) {
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}

	pj := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *refs), labels)
	logger.WithFields(pjutil.ProwJobFields(&pj)).Info("Submitting a new prowjob.")

	if dry {
		jobAsYAML, err := yaml.Marshal(pj)
		if err != nil {
			return nil, fmt.Errorf("Failed to marshal job to YAML: %v", err)
		}
		fmt.Printf("%s\n", jobAsYAML)
		return &pj, nil
	}

	return pjclient.Create(&pj)
}

func ExecuteJobs(toBeRehearsed map[string][]prowconfig.Presubmit, prNumber int, refs *pjapi.Refs, logger logrus.FieldLogger, rehearsalConfigs CIOperatorConfigs, pjclient pj.ProwJobInterface, dry bool) error {
	rehearsals := []*prowconfig.Presubmit{}

	for repo, jobs := range toBeRehearsed {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, repo, prNumber)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
			} else {
				jobLogger.WithField("rehearsal-job", rehearsal.Name).Info("Created a rehearsal job to be submitted")
				rehearsalConfigs.FixupJob(rehearsal, repo)
				rehearsals = append(rehearsals, rehearsal)
			}
		}
	}

	if len(rehearsals) > 0 {
		if err := rehearsalConfigs.Create(); err != nil {
			return fmt.Errorf("failed to prepare rehearsal ci-operator config ConfigMap: %v", err)
		}
		for _, job := range rehearsals {
			created, err := submitRehearsal(job, refs, logger, pjclient, dry)
			if err != nil {
				logger.WithError(err).Warn("Failed to execute a rehearsal presubmit presubmit")
			} else {
				logger.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
			}
		}
	} else {
		logger.Warn("No job rehearsals")
	}

	return nil
}
