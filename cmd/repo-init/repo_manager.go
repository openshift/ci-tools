package main

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
)

type repoManager struct {
	mux            sync.Mutex
	numRepos       int
	availableRepos []*repo
	inUseRepos     []*repo
}

type repo struct {
	path    string
	inUseBy string
}

func (rm *repoManager) init() {
	logrus.SetLevel(logrus.DebugLevel)

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: secret.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: secret.Censor}

	repoChannel := make(chan *repo)
	for i := 0; i < rm.numRepos; i++ {
		go func(repoChannel chan *repo) {
			repo := initRepo(stdout, stderr)
			repoChannel <- repo
			logrus.Debugf("Initialized repo %v", repo)
		}(repoChannel)
	}
	for i := 0; i < rm.numRepos; i++ {
		rm.availableRepos = append(rm.availableRepos, <-repoChannel)
	}

	logrus.Debugf("Done initializing repos. %v", rm.availableRepos)
}

func initRepo(stdout, stderr bumper.HideSecretsWriter) *repo {
	path, err := os.MkdirTemp("", "repo-manager-release")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to make dir.")
	}
	thisRepo := repo{
		path: path,
	}

	err = bumper.Call(stdout, stderr, "git", []string{"clone", "https://github.com/openshift/release.git", thisRepo.path})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to clone repo.")
	}

	return &thisRepo
}

// retrieveAndLockAvailable obtains an available repo (if one exists) and assigns it to the specified githubUsername.
func (rm *repoManager) retrieveAndLockAvailable(githubUsername string) (repository *repo, err error) {
	rm.mux.Lock()
	// since repositories are almost always in use for a very short time, try a handful of times before we abort.
	err = wait.ExponentialBackoff(wait.Backoff{Duration: time.Second, Factor: 2, Steps: 5}, func() (done bool, err error) {
		repository, err = func() (*repo, error) {
			if len(rm.availableRepos) == 0 {
				return nil, fmt.Errorf("all repositories are currently in use")
			}
			lastIndex := len(rm.availableRepos) - 1
			availableRepo := rm.availableRepos[lastIndex]
			rm.availableRepos = rm.availableRepos[:lastIndex]
			availableRepo.inUseBy = githubUsername
			rm.inUseRepos = append(rm.inUseRepos, availableRepo)
			// make sure we update the repo to the latest changes before giving it out.
			err := updateRepo(availableRepo)
			if err != nil {
				return nil, fmt.Errorf("unable to lock and sync repo: %w", err)
			}

			return availableRepo, nil
		}()
		return repository != nil, err
	})

	rm.mux.Unlock()
	return repository, err
}

func (rm *repoManager) returnInUse(r *repo) {
	rm.mux.Lock()
	for i, cr := range rm.inUseRepos {
		if r == cr {
			rm.inUseRepos = append(rm.inUseRepos[0:i], rm.inUseRepos[i+1:]...)
			r.inUseBy = ""
			rm.availableRepos = append(rm.availableRepos, r)
		}
	}
	rm.mux.Unlock()
}

func updateRepo(repo *repo) error {
	err := os.Chdir(repo.path)
	if err != nil {
		logrus.WithError(err).Error("can't change dir")
		return err
	}
	logrus.Debugf("Pulling latest changes")

	if err := bumper.Call(os.Stdout,
		os.Stderr,
		"git",
		[]string{"pull", "origin", "main"}); err != nil {
		return fmt.Errorf("failed to pull latest changes: %w", err)
	}

	return nil
}

func pushChanges(gitRepo *repo, githubOptions flagutil.GitHubOptions, org, repo, githubUsername, githubToken string, createPR bool) (string, error) {
	if err := updateRepo(gitRepo); err != nil {
		logrus.WithError(err).Error("unable to update repo")
		return "", err
	}

	logrus.Debugf("Pushing changes")

	if err := commitChanges(
		"Adding new ci-operator config.",
		fmt.Sprintf("%s@users.noreply.github.com", githubUsername),
		githubUsername,
	); err != nil {
		return "", fmt.Errorf("failed to commit changes: %w", err)
	}

	targetBranch := fmt.Sprintf("new-ci-config-%s", strconv.FormatInt(time.Now().Unix(), 10))
	if err := bumper.GitPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/release.git", githubUsername, githubToken, githubUsername),
		targetBranch,
		os.Stdout,
		os.Stderr,
		gitRepo.path,
	); err != nil {
		return "", fmt.Errorf("failed to push changes: %w", err)
	}

	if createPR {
		ghClient, err := githubOptions.GitHubClientWithAccessToken(githubToken)
		if err != nil {
			return "", fmt.Errorf("failed to create github client: %w", err)
		}

		if err := bumper.UpdatePullRequestWithLabels(
			ghClient,
			"openshift",
			"release",
			fmt.Sprintf("New CI Operator config for %s/%s", org, repo),
			"PR auto-generated via Repo Initializer tool.",
			githubUsername+":"+targetBranch,
			"master",
			targetBranch,
			true,
			nil,
			false,
		); err != nil {
			return "", fmt.Errorf("failed to create PR: %w", err)
		}

	}

	logrus.Debugf("Resetting local repository.")
	if err := bumper.Call(os.Stdout,
		os.Stderr,
		"git",
		[]string{"reset", "--hard", "origin/main"}); err != nil {
		return "", fmt.Errorf("failed to reset local: %w", err)
	}

	if err := bumper.Call(os.Stdout,
		os.Stderr,
		"git",
		[]string{"clean", "-df"}); err != nil {
		return "", fmt.Errorf("failed to clean local: %w", err)
	}

	return targetBranch, nil
}

func commitChanges(message, email, name string) error {
	if err := bumper.Call(os.Stdout, os.Stderr, "git", []string{"add", "-A"}); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	if err := bumper.Call(os.Stdout, os.Stderr, "git", []string{"config", "--local", "user.email", email}); err != nil {
		return fmt.Errorf("failed to configure email address: %w", err)
	}
	if err := bumper.Call(os.Stdout, os.Stderr, "git", []string{"config", "--local", "user.name", name}); err != nil {
		return fmt.Errorf("failed to configure email address: %w", err)
	}
	if err := bumper.Call(os.Stdout, os.Stderr, "git", []string{"config", "--local", "commit.gpgsign", "false"}); err != nil {
		return fmt.Errorf("failed to configure disabling gpg signing: %w", err)
	}

	author := fmt.Sprintf("%s <%s>", name, email)
	commitArgs := []string{"commit", "-m", message, "--author", author}

	if err := bumper.Call(os.Stdout, os.Stderr, "git", commitArgs); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}
