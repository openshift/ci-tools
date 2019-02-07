package rehearse

import (
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"
)

type Result struct {
	Name   string
	Status pjapi.ProwJobStatus
}

type Executor struct {
	dryRun     bool
	rehearsals []*prowconfig.Presubmit
	prNumber   int
	prRepo     string
	refs       *pjapi.Refs
	loggers    Loggers
	pjclient   pj.ProwJobInterface
	resultChan chan Result
	errorChan  chan error
	quit       chan struct{}
}

func NewExecutor(toBeRehearsed map[string][]prowconfig.Presubmit, prNumber int, prRepo string, refs *pjapi.Refs,
	loggers Loggers, pjclient pj.ProwJobInterface, dryRun bool, resultChan chan Result, errorChan chan error, quit chan struct{}) *Executor {
	rehearsals := configureRehearsalJobs(toBeRehearsed, prRepo, prNumber, loggers)
	return &Executor{
		dryRun:     dryRun,
		rehearsals: rehearsals,
		prNumber:   prNumber,
		prRepo:     prRepo,
		refs:       refs,
		loggers:    loggers,
		pjclient:   pjclient,
		resultChan: resultChan,
		errorChan:  errorChan,
		quit:       quit,
	}
}

// ExecuteJobs takes configs for a set of jobs which should be "rehearsed", and
// creates the ProwJobs that perform the actual rehearsal. *Rehearsal* means
// a "trial" execution of a Prow job configuration when the *job config* config
// is changed, giving feedback to Prow config authors on how the changes of the
// config would affect the "production" Prow jobs run on the actual target repos
func (e *Executor) ExecuteJobs() {
	pjs := e.submitRehearsals()
	selector, err := e.getExecutionSelector()
	if err != nil {
		e.errorChan <- err
	}

	if !e.dryRun && len(pjs) > 0 {
		if err := e.waitForJobs(pjs, selector); err != nil {
			e.errorChan <- err
			return
		}
	}

	e.quit <- struct{}{}
}

func (e *Executor) waitForJobs(jobs sets.String, selector string) error {
	for {
		w, err := e.pjclient.Watch(metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return fmt.Errorf("failed to create watch for ProwJobs: %v", err)
		}
		defer w.Stop()

		for event := range w.ResultChan() {
			pj, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				return fmt.Errorf("received a %T from watch", event.Object)
			}
			fields := pjutil.ProwJobFields(pj)
			fields["state"] = pj.Status.State
			e.loggers.Debug.WithFields(fields).Debug("Processing ProwJob")
			if !jobs.Has(pj.Name) {
				continue
			}

			e.resultChan <- Result{
				Name:   pj.Name,
				Status: pj.Status,
			}

			switch pj.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				e.loggers.Job.WithFields(fields).Error("Job failed")
			case pjapi.SuccessState:
				e.loggers.Job.WithFields(fields).Info("Job succeeded")
			default:
				continue
			}
			jobs.Delete(pj.Name)
			if jobs.Len() == 0 {
				return nil
			}
		}
	}
}

func (e *Executor) submitRehearsals() sets.String {
	pjs := make(sets.String, len(e.rehearsals))
	for _, job := range e.rehearsals {
		created, err := e.submitRehearsal(job)
		if err != nil {
			e.loggers.Job.WithError(err).Warn("Failed to execute a rehearsal presubmit")
			continue
		}
		e.loggers.Job.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
		pjs.Insert(created.Name)
	}
	return pjs
}

func (e *Executor) submitRehearsal(job *prowconfig.Presubmit) (*pjapi.ProwJob, error) {
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}

	prowJob := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *e.refs), labels)
	e.loggers.Job.WithFields(pjutil.ProwJobFields(&prowJob)).Info("Submitting a new prowjob.")

	return e.pjclient.Create(&prowJob)
}

func (e *Executor) getExecutionSelector() (string, error) {
	req, err := labels.NewRequirement(rehearseLabel, selection.Equals, []string{strconv.Itoa(e.prNumber)})
	if err != nil {
		return "", fmt.Errorf("failed to create label selector: %v", err)
	}
	return labels.NewSelector().Add(*req).String(), nil
}
