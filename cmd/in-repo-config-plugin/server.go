package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pluginhelp"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

const ciOperatorDir = ".ci-operator"

var pluginGenerator = jc.Generator(pluginName)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetDirectory(org, repo, dirpath, commit string) ([]github.DirectoryContent, error)
	GetFile(org, repo, filepath, commit string) ([]byte, error)
}

type server struct {
	ghc              githubClient
	pjclient         ctrlruntimeclient.Client
	prowConfigPath   string
	namespace        string
	jobConfigDir     string
	prowgenImage     string
	checkconfigImage string
	dispatcher       dispatcher.Client
	efsLocks         sync.Map
}

func (s *server) repoLock(org, repo string) *sync.Mutex {
	key := org + "/" + repo
	val, _ := s.efsLocks.LoadOrStore(key, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func (s *server) comment(l *logrus.Entry, org, repo string, number int, msg string) {
	if err := s.ghc.CreateComment(org, repo, number, msg); err != nil {
		l.WithError(err).Error("failed to post comment")
	}
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return &pluginhelp.PluginHelp{
		Description: "The in-repo-config plugin automatically manages CI jobs for repos using in-repo .ci-operator/ configuration. " +
			"It writes ephemeral ProwJob definitions for new tests on PRs, " +
			"writes permanent definitions on merge, and auto-onboards repos on first push.",
	}, nil
}

// --- Pull request handling ---

func (s *server) handlePullRequest(l *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number
	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "pr": number, "action": pre.Action,
	})

	switch pre.Action {
	case github.PullRequestActionOpened, github.PullRequestActionSynchronize, github.PullRequestActionReopened:
		s.handlePROpenedOrUpdated(logger, pre)
	case github.PullRequestActionClosed:
		s.handlePRClosed(logger, pre)
	}
}

func (s *server) handlePROpenedOrUpdated(logger *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number
	sha := pre.PullRequest.Head.SHA
	branch := pre.PullRequest.Base.Ref

	allJobs, useDir, err := s.generateAllJobs(org, repo, branch, sha, logger)
	if err != nil {
		logger.WithError(err).Error("could not generate jobs")
		s.comment(logger, org, repo, number,
			fmt.Sprintf("Error generating jobs at commit %s. Please check plugin logs.", shortSHA(sha)))
		return
	}
	if allJobs == nil {
		return
	}

	params := newBootstrapParams(org, repo, branch, useDir, s.prowgenImage, s.checkconfigImage)
	if bootstrapJobs, err := generateBootstrapJobs(params); err != nil {
		logger.WithError(err).Warn("could not generate bootstrap jobs")
	} else {
		jc.Append(allJobs, bootstrapJobs)
	}

	_, newPresubmits, newPeriodics := filterNewJobs(allJobs, s.jobConfigDir, org, repo, logger)
	if len(newPresubmits) == 0 && len(newPeriodics) == 0 {
		logger.Info("no new tests detected, skipping ephemeral write")
		return
	}

	s.writeEphemeralAndTrigger(logger, pre, number, newPresubmits, newPeriodics, sha)
}

func (s *server) writeEphemeralAndTrigger(logger *logrus.Entry, pre github.PullRequestEvent, number int, presubmits []prowconfig.Presubmit, periodics []prowconfig.Periodic, sha string) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	orgrepo := fmt.Sprintf("%s/%s", org, repo)

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	ephemeralDir := filepath.Join(s.jobConfigDir, "ephemeral", org, repo, fmt.Sprintf("PR-%d", number))
	if err := os.RemoveAll(ephemeralDir); err != nil {
		logger.WithError(err).Warn("could not clean old ephemeral directory")
	}

	ephemeralJobs := &prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{orgrepo: presubmits},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
		Periodics:         periodics,
	}
	if err := writeEphemeralJobs(ephemeralDir, org, repo, number, ephemeralJobs); err != nil {
		logger.WithError(err).Error("could not write ephemeral jobs to EFS")
		s.comment(logger, org, repo, number, "Error writing ephemeral job definitions. Please check plugin logs.")
		return
	}

	triggered := s.triggerJobs(logger, pre, orgrepo, presubmits, periodics)
	s.commentNewJobs(logger, org, repo, number, sha, presubmits, periodics, triggered)
	logger.WithField("jobs", len(presubmits)+len(periodics)).WithField("triggered", len(triggered)).Info("ephemeral jobs written and triggered")
}

func (s *server) handlePRClosed(logger *logrus.Entry, pre github.PullRequestEvent) {
	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	number := pre.Number

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	ephemeralDir := filepath.Join(s.jobConfigDir, "ephemeral", org, repo, fmt.Sprintf("PR-%d", number))
	if err := os.RemoveAll(ephemeralDir); err != nil {
		logger.WithError(err).Error("could not clean up ephemeral jobs")
	} else {
		logger.Info("cleaned up ephemeral jobs for closed PR")
	}
}

// --- Push handling ---

func (s *server) handlePush(l *logrus.Entry, pe github.PushEvent) {
	if pe.Deleted || !strings.HasPrefix(pe.Ref, "refs/heads/") {
		return
	}

	org := pe.Repo.Owner.Login
	repo := pe.Repo.Name
	branch := pe.Branch()
	sha := pe.After
	logger := l.WithFields(logrus.Fields{
		"org": org, "repo": repo, "branch": branch, "sha": sha,
	})

	if !pushTouchesCIOperator(pe) {
		return
	}

	if pe.Repo.DefaultBranch != "" && branch != pe.Repo.DefaultBranch {
		logger.Debug("ignoring push to non-default branch")
		return
	}

	logger.Info("push touches .ci-operator/ configs, generating permanent jobs")

	allJobs, useDir, err := s.generateAllJobs(org, repo, branch, sha, logger)
	if err != nil {
		logger.WithError(err).Error("could not fetch or generate jobs from pushed commit")
		return
	}
	if allJobs == nil {
		logger.Warn("push touched .ci-operator/ but no configs found")
		return
	}

	lock := s.repoLock(org, repo)
	lock.Lock()
	defer lock.Unlock()

	params := newBootstrapParams(org, repo, branch, useDir, s.prowgenImage, s.checkconfigImage)
	if bootstrapJobs, err := generateBootstrapJobs(params); err != nil {
		logger.WithError(err).Error("could not generate bootstrap jobs")
		return
	} else {
		jc.Append(allJobs, bootstrapJobs)
	}

	if err := jc.WriteBranchToDir(s.jobConfigDir, org, repo, allJobs, pluginGenerator); err != nil {
		logger.WithError(err).Error("could not write jobs to EFS")
		return
	}
	logger.Info("jobs written to EFS")
}
