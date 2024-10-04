package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
)

var configLock sync.Mutex

type reportGithubClient interface {
	CreateCheckRun(org, repo string, checkRun github.CheckRun) (int64, error)
	UpdateCheckRun(org, repo string, checkRunId int64, checkRun github.CheckRun) error
}

func newReporter(githubClient github.Client, kubeClient ctrlruntimeclient.Client, namespace string, jobConfigFile string) reporter {
	r := reporter{
		kubeClient:    kubeClient,
		ghc:           githubClient,
		namespace:     namespace,
		jobConfigFile: jobConfigFile,
	}
	if err := r.ensureJobConfigFile(); err != nil {
		logrus.WithError(err).Fatal("error ensuring job config file")
	}

	return r
}

type Reporter interface {
	reportNewProwJob(prowJob *prowv1.ProwJob, jr jobRun, logger *logrus.Entry) error
	sync(logger *logrus.Entry) error
}

type reporter struct {
	kubeClient ctrlruntimeclient.Client
	ghc        reportGithubClient

	namespace     string
	jobConfigFile string
}

type Config struct {
	Jobs []Job `json:"jobs"`
}

type Job struct {
	ProwJobID       string `json:"prowjob_id"`
	CheckRunDetails `json:"check_run_details"`
	Org             string    `json:"org"`
	Repo            string    `json:"repo"`
	CreatedAt       time.Time `json:"created_at"`
}

type CheckRunDetails struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (r *reporter) reportNewProwJob(prowJob *prowv1.ProwJob, jr jobRun, logger *logrus.Entry) error {
	key := ctrlruntimeclient.ObjectKey{Namespace: r.namespace, Name: prowJob.ObjectMeta.Name}
	created := &prowv1.ProwJob{}
	if err := wait.PollUntilContextTimeout(context.Background(), time.Second*5, time.Second*60, true, func(ctx context.Context) (bool, error) {
		if err := r.kubeClient.Get(context.Background(), key, created); err != nil {
			if kerrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("getting prowJob failed: %w", err)
		}
		if created.Status.URL == "" {
			logger.Debug("no url found in prowjob")
			return false, nil
		}

		text := fmt.Sprintf("[Job logs and status](%s)\nIncluded PRs: \n", created.Status.URL)
		for _, pr := range jr.AdditionalPRs {
			text += fmt.Sprintf("* %s/%s#%d\n", pr.Base.Repo.Owner.Login, pr.Base.Repo.Name, pr.Number)
		}
		checkRun := github.CheckRun{
			HeadSHA: jr.OriginPR.Head.SHA,
			Status:  "in_progress",
			Output: github.CheckRunOutput{
				Title:   created.Spec.Job,
				Summary: "Job Triggered",
				Text:    text,
			},
			Name: created.Spec.Job,
		}

		org := jr.OriginPR.Base.Repo.Owner.Login
		repo := jr.OriginPR.Base.Repo.Name
		id, err := r.ghc.CreateCheckRun(org, repo, checkRun)
		if err != nil {
			logger.WithError(err).Error("could not create check run")
			return false, fmt.Errorf("could not create check run: %w", err)
		}
		job := Job{
			ProwJobID: created.Name,
			CheckRunDetails: CheckRunDetails{
				ID:    id,
				Title: checkRun.Output.Title,
				Text:  checkRun.Output.Text,
			},
			Org:       org,
			Repo:      repo,
			CreatedAt: created.CreationTimestamp.Time,
		}
		if err := r.addJobToConfig(job, logger); err != nil {
			logger.WithError(err).Error("could not write job config")
			return false, fmt.Errorf("could not write job config: %w", err)
		}
		return true, nil
	}); err != nil {
		logger.WithError(err).Error("could not successfully report new prowjob")
		return fmt.Errorf("could not successfully report new prowjob: %w", err)
	}

	return nil
}

var (
	completedStates   = []prowv1.ProwJobState{prowv1.SuccessState, prowv1.FailureState, prowv1.ErrorState, prowv1.AbortedState}
	stateToConclusion = map[prowv1.ProwJobState]string{
		prowv1.SuccessState: "success",
		prowv1.FailureState: "failure",
		prowv1.ErrorState:   "failure",
		prowv1.AbortedState: "cancelled",
	}
)

func (r *reporter) sync(logger *logrus.Entry) error {
	logger.Info("syncing jobs")
	configLock.Lock()
	defer configLock.Unlock()
	config, err := r.getConfig()
	if err != nil {
		return fmt.Errorf("error syncing: %w", err)
	}

	var errs []error
	for i := len(config.Jobs) - 1; i >= 0; i-- {
		job := config.Jobs[i]
		jobLogger := logger.WithField("job", job.ProwJobID)
		jobLogger.Debug("syncing job")
		// If the job was created over 25 hours ago, we should remove it from the config
		if time.Now().Add(time.Hour * -25).After(job.CreatedAt) {
			jobLogger.Warn("job was created over 25 hours ago, this could point to a syncing issue. removing job from config")
			config.Jobs = append(config.Jobs[:i], config.Jobs[i+1:]...)
		}
		key := ctrlruntimeclient.ObjectKey{Name: job.ProwJobID, Namespace: r.namespace}
		prowJob := &prowv1.ProwJob{}
		if err := r.kubeClient.Get(context.Background(), key, prowJob); err != nil {
			if kerrors.IsNotFound(err) {
				jobLogger.Info("job not found, removing from config")
				config.Jobs = append(config.Jobs[:i], config.Jobs[i+1:]...)
				continue
			}
			errs = append(errs, fmt.Errorf("error getting prowjob: %s: %w", job.ProwJobID, err))
		}
		jobLogger.Debugf("status of: %s", string(prowJob.Status.State))
		if slices.Contains(completedStates, prowJob.Status.State) {
			jobLogger.Debugf("creating a new checkRun as status is now %s", prowJob.Status.State)
			checkRun := github.CheckRun{
				Conclusion: stateToConclusion[prowJob.Status.State],
				Output: github.CheckRunOutput{
					Title:   job.CheckRunDetails.Title,
					Summary: "Job Finished",
					Text:    job.CheckRunDetails.Text,
				},
			}
			if err := r.ghc.UpdateCheckRun(job.Org, job.Repo, job.CheckRunDetails.ID, checkRun); err != nil {
				jobLogger.WithError(err).Error("could not update check run")
				errs = append(errs, err)
			}
			config.Jobs = append(config.Jobs[:i], config.Jobs[i+1:]...)
		}

	}

	if err := r.updateConfig(config, logger); err != nil {
		errs = append(errs, fmt.Errorf("error updating config: %w", err))
	}

	return utilerrors.NewAggregate(errs)
}

func (r *reporter) addJobToConfig(job Job, logger *logrus.Entry) error {
	logger.Debugf("adding job to config: %s", job.ProwJobID)
	configLock.Lock()
	defer configLock.Unlock()
	config, err := r.getConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	config.Jobs = append(config.Jobs, job)
	marshalled, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshalling config: %w", err)
	}
	if err := os.WriteFile(r.jobConfigFile, marshalled, 0644); err != nil {
		return fmt.Errorf("error writing config: %w", err)
	}

	return nil
}

func (r *reporter) ensureJobConfigFile() error {
	configLock.Lock()
	defer configLock.Unlock()
	_, err := os.ReadFile(r.jobConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c := &Config{}
			marshalled, err := json.Marshal(c)
			if err != nil {
				return fmt.Errorf("error marshalling config: %w", err)
			}
			if err := os.WriteFile(r.jobConfigFile, marshalled, 0644); err != nil {
				return fmt.Errorf("error writing config: %w", err)
			}
		} else {
			return fmt.Errorf("other error reading job config file: %w", err)
		}
	}

	return nil
}

func (r *reporter) updateConfig(config *Config, logger *logrus.Entry) error {
	logger.Debug("updating config")
	marshalled, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("error marshalling config: %w", err)
	}
	if err := os.WriteFile(r.jobConfigFile, marshalled, 0644); err != nil {
		return fmt.Errorf("error writing config: %w", err)
	}

	return nil
}

func (r *reporter) getConfig() (*Config, error) {
	c := &Config{}
	rawJobs, err := os.ReadFile(r.jobConfigFile)
	if err != nil {
		return nil, fmt.Errorf("error reading job config: %w", err)
	}
	if len(rawJobs) > 0 {
		if err := json.Unmarshal(rawJobs, c); err != nil {
			return nil, fmt.Errorf("error unmarshalling job config: %w", err)
		}
	}
	return c, nil
}
