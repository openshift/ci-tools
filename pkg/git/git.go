/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package git provides a client to plugins that can do git operations.
// This has been forked from kubernetes/test-infra as the v2 git client doesn't have the functionality we need
package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/git/types"
)

const github = "github.com"

// Client can clone repos. It keeps a local cache, so successive clones of the
// same repo should be quick. Create with NewClient. Be sure to clean it up.
type Client struct {
	// logger will be used to log git operations and must be set.
	logger *logrus.Entry

	credLock sync.RWMutex
	// user is used when pushing or pulling code if specified.
	user string

	// needed to generate the token.
	tokenGenerator GitTokenGenerator

	// dir is the location of the git cache.
	dir string
	// git is the path to the git binary.
	git string
	// base is the base path for git clone calls. For users it will be set to
	// GitHub, but for tests set it to a directory with git repos.
	base string
	// host is the git host.
	// TODO: use either base or host. the redundancy here is to help landing
	// #14609 easier.
	host string

	// The mutex protects repoLocks which protect individual repos. This is
	// necessary because Clone calls for the same repo are racy. Rather than
	// one lock for all repos, use a lock per repo.
	// Lock with Client.lockRepo, unlock with Client.unlockRepo.
	rlm       sync.Mutex
	repoLocks map[string]*sync.Mutex
}

// Clean removes the local repo cache. The Client is unusable after calling.
func (c *Client) Clean() error {
	return os.RemoveAll(c.dir)
}

type GitTokenGenerator func(org string) (string, error)

func (c *Client) lockRepo(repo string) {
	c.rlm.Lock()
	if _, ok := c.repoLocks[repo]; !ok {
		c.repoLocks[repo] = &sync.Mutex{}
	}
	m := c.repoLocks[repo]
	c.rlm.Unlock()
	m.Lock()
}

func (c *Client) unlockRepo(repo string) {
	c.rlm.Lock()
	defer c.rlm.Unlock()
	c.repoLocks[repo].Unlock()
}

// refreshRepoAuth updates Repo client token when current token is going to expire.
// Git client authenticating with PAT(personal access token) doesn't have this problem as it's a single token.
// GitHub app auth will need this for rotating token every hour.
func (r *Repo) refreshRepoAuth() error {
	// Lock because we'll update r.pass here
	r.credLock.Lock()
	defer r.credLock.Unlock()
	pass, err := r.tokenGenerator(r.org)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}
	if pass == r.pass { // Token unchanged, no need to do anything
		return nil
	}

	r.pass = pass
	remote := remoteFromBase(r.base, r.user, r.pass, r.host, r.org, r.repo)
	if b, err := r.gitCommand("remote", "set-url", "origin", remote).CombinedOutput(); err != nil {
		return fmt.Errorf("updating remote url failed: %w. output: %s", err, string(b))
	}
	return nil
}

func remoteFromBase(base, user, pass, host, org, repo string) string {
	baseWithAuth := base
	if user != "" && pass != "" {
		baseWithAuth = fmt.Sprintf("https://%s:%s@%s", user, pass, host)
	}
	return fmt.Sprintf("%s/%s/%s", baseWithAuth, org, repo)
}

// Repo is a clone of a git repository. Create with Client.Clone, and don't
// forget to clean it up after.
type Repo struct {
	// dir is the location of the git repo.
	dir string

	// git is the path to the git binary.
	git string
	// host is the git host.
	host string
	// base is the base path for remote git fetch calls.
	base string
	// org is the organization name: "org" in "org/repo".
	org string
	// repo is the repository name: "repo" in "org/repo".
	repo string
	// user is used for pushing to the remote repo.
	user string
	// pass is used for pushing to the remote repo.
	pass string

	// needed to generate the token.
	tokenGenerator GitTokenGenerator

	credLock sync.RWMutex

	logger *logrus.Entry
}

// Directory exposes the location of the git repo
func (r *Repo) Directory() string {
	return r.dir
}

// Clean deletes the repo. It is unusable after calling.
func (r *Repo) Clean() error {
	return os.RemoveAll(r.dir)
}

func (r *Repo) gitCommand(arg ...string) *exec.Cmd {
	cmd := exec.Command(r.git, arg...)
	cmd.Dir = r.dir
	r.logger.WithField("args", cmd.Args).WithField("dir", cmd.Dir).Debug("Constructed git command")
	return cmd
}

// RevParse runs git rev-parse.
func (r *Repo) RevParse(commitlike string) (string, error) {
	r.logger.WithField("commitlike", commitlike).Info("RevParse.")
	b, err := r.gitCommand("rev-parse", commitlike).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error rev-parsing %s: %v. output: %s", commitlike, err, string(b))
	}
	return string(b), nil
}

// MergeWithStrategy attempts to merge commitlike into the current branch given the merge strategy.
// It returns true if the merge completes. It returns an error if the abort fails.
func (r *Repo) MergeWithStrategy(commitlike string, mergeStrategy types.PullRequestMergeType) (bool, error) {
	r.logger.WithField("commitlike", commitlike).Info("Merging.")
	switch mergeStrategy {
	case types.MergeMerge:
		return r.mergeWithMergeStrategyMerge(commitlike)
	case types.MergeSquash:
		return r.mergeWithMergeStrategySquash(commitlike)
	case types.MergeRebase:
		return r.mergeWithMergeStrategyRebase(commitlike)
	default:
		return false, fmt.Errorf("merge strategy %q is not supported", mergeStrategy)
	}
}

func (r *Repo) mergeWithMergeStrategyMerge(commitlike string) (bool, error) {
	co := r.gitCommand("merge", "--no-ff", "--no-stat", "-m merge", commitlike)

	b, err := co.CombinedOutput()
	if err == nil {
		return true, nil
	}
	r.logger.WithField("out", string(b)).WithError(err).Infof("Merge failed.")

	if b, err := r.gitCommand("merge", "--abort").CombinedOutput(); err != nil {
		return false, fmt.Errorf("error aborting merge for commitlike %s: %v. output: %s", commitlike, err, string(b))
	}

	return false, nil
}

func (r *Repo) mergeWithMergeStrategySquash(commitlike string) (bool, error) {
	co := r.gitCommand("merge", "--squash", "--no-stat", commitlike)

	b, err := co.CombinedOutput()
	if err != nil {
		r.logger.WithField("out", string(b)).WithError(err).Infof("Merge failed.")
		if b, err := r.gitCommand("reset", "--hard", "HEAD").CombinedOutput(); err != nil {
			return false, fmt.Errorf("error resetting after failed squash for commitlike %s: %v. output: %s", commitlike, err, string(b))
		}
		return false, nil
	}

	b, err = r.gitCommand("commit", "--no-stat", "-m", "merge").CombinedOutput()
	if err != nil {
		r.logger.WithField("out", string(b)).WithError(err).Infof("Commit after squash failed.")
		return false, err
	}

	return true, nil
}

func (r *Repo) mergeWithMergeStrategyRebase(commitlike string) (bool, error) {
	if commitlike == "" {
		return false, errors.New("branch must be set")
	}

	headRev, err := r.revParse("HEAD")
	if err != nil {
		r.logger.WithError(err).Infof("Failed to parse HEAD revision")
		return false, err
	}
	headRev = strings.TrimSuffix(headRev, "\n")

	co := r.gitCommand("rebase", "--no-stat", headRev, commitlike)
	b, err := co.CombinedOutput()
	if err != nil {
		r.logger.WithField("out", string(b)).WithError(err).Infof("Rebase failed.")
		if b, err := r.gitCommand("rebase", "--abort").CombinedOutput(); err != nil {
			return false, fmt.Errorf("error aborting after failed rebase for commitlike %s: %v. output: %s", commitlike, err, string(b))
		}
		return false, nil
	}

	return true, nil
}

func (r *Repo) revParse(args ...string) (string, error) {
	fullArgs := append([]string{"rev-parse"}, args...)
	co := r.gitCommand(fullArgs...)
	b, err := co.CombinedOutput()
	if err != nil {
		return "", errors.New(string(b))
	}
	return string(b), nil
}

// CheckoutPullRequest does exactly that.
func (r *Repo) CheckoutPullRequest(number int) error {
	if err := r.refreshRepoAuth(); err != nil {
		return err
	}
	r.logger.WithFields(logrus.Fields{"org": r.org, "repo": r.repo, "number": number}).Info("Fetching and checking out.")
	remote := remoteFromBase(r.base, r.user, r.pass, r.host, r.org, r.repo)
	if b, err := retryCmd(r.logger, r.dir, r.git, "fetch", remote, fmt.Sprintf("pull/%d/head:pull%d", number, number)); err != nil {
		return fmt.Errorf("git fetch failed for PR %d: %v. output: %s", number, err, string(b))
	}
	co := r.gitCommand("checkout", fmt.Sprintf("pull%d", number))
	if b, err := co.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout failed for PR %d: %v. output: %s", number, err, string(b))
	}
	return nil
}

// Config runs git config.
func (r *Repo) Config(args ...string) error {
	r.logger.WithField("args", args).Info("Running git config.")
	if b, err := r.gitCommand(append([]string{"config"}, args...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("git config %v failed: %v. output: %s", args, err, string(b))
	}
	return nil
}

// retryCmd will retry the command a few times with backoff. Use this for any
// commands that will be talking to GitHub, such as clones or fetches.
func retryCmd(l *logrus.Entry, dir, cmd string, arg ...string) ([]byte, error) {
	var b []byte
	var err error
	sleepyTime := time.Second
	for i := 0; i < 3; i++ {
		c := exec.Command(cmd, arg...)
		c.Dir = dir
		b, err = c.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("running %q %v returned error %w with output %q", cmd, arg, err, string(b))
			l.WithField("count", i+1).WithError(err).Debug("Retrying, if this is not the 3rd try then this will be retried.")
			time.Sleep(sleepyTime)
			sleepyTime *= 2
			continue
		}
		break
	}
	return b, err
}
