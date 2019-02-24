package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
	"github.com/openshift/ci-operator/pkg/api"
)

type options struct {
	promotion.Options
	gitDir   string
	username string
	password string
}

func (o *options) Validate() error {
	if err := o.Options.Validate(); err != nil {
		return err
	}
	if o.Confirm {
		if o.username == "" {
			return errors.New("--username is required with --confirm")
		}
		if o.password == "" {
			return errors.New("--password is required with --confirm")
		}
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.gitDir, "git-dir", "", "Optional dir to do git operations in. If unset, temp dir will be used.")
	fs.StringVar(&o.username, "username", "", "Username to use when pushing to GitHub.")
	fs.StringVar(&o.password, "password", "", "Password to use when pushing to GitHub.")
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	gitDir := o.gitDir
	if gitDir == "" {
		tempDir, err := ioutil.TempDir("", "")
		if err != nil {
			logrus.WithError(err).Fatal("Could not create temp dir for git operations")
		}
		defer func() {
			if err := os.Remove(tempDir); err != nil {
				logrus.WithError(err).Fatal("Could not clean up temp dir for git operations")
			}
		}()
		gitDir = tempDir
	}

	if err := config.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.FilePathElements) error {
		logger := config.LoggerForInfo(*repoInfo)
		if !(promotion.PromotesOfficialImages(configuration) && configuration.PromotionConfiguration.Name == o.CurrentRelease) {
			return nil
		}

		repoDir := path.Join(gitDir, repoInfo.Org, repoInfo.Repo)
		if err := os.MkdirAll(repoDir, 0775); err != nil {
			logger.WithError(err).Fatal("could not ensure git dir existed")
			return nil
		}

		futureBranchForCurrentPromotion, futureBranchForFuturePromotion, err := promotion.DetermineReleaseBranches(o.CurrentRelease, o.FutureRelease, repoInfo.Branch)
		if err != nil {
			logger.WithError(err).Error("could not determine future branch that would promote to current imagestream")
			return nil
		}

		remote, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", repoInfo.Org, repoInfo.Repo))
		if err != nil {
			logger.WithError(err).Fatal("Could not construct remote URL.")
		}
		if o.Confirm {
			remote.User = url.UserPassword(o.username, o.password)
		}
		if err := execute([]string{"init"}, repoDir, logger); err != nil {
			os.Exit(1)
		}
		if err := execute([]string{"fetch", "--depth", "1", remote.String(), repoInfo.Branch}, repoDir, logger); err != nil {
			os.Exit(1)
		}

		// when we're initializing the branch, we just want to make sure
		// it is in sync with the current branch that is promoting
		for _, futureBranch := range []string{futureBranchForCurrentPromotion, futureBranchForFuturePromotion} {
			if futureBranch == repoInfo.Branch {
				continue
			}
			branchLogger := logger.WithField("future-branch", futureBranch)
			if err := execute([]string{"ls-remote", remote.String(), fmt.Sprintf("refs/heads/%s", futureBranch)}, repoDir, logger); err == nil {
				branchLogger.Info("Remote already has branch, skipping.")
				continue
			}
			if !o.Confirm {
				branchLogger.Info("Would create new branch.")
				continue
			}
			if err := execute([]string{"push", remote.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", futureBranch)}, repoDir, logger); err != nil {
				os.Exit(1)
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}
}

func execute(command []string, dir string, logger *logrus.Entry) error {
	cmdLogger := logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
	cmd := exec.Command("git", command...)
	cmd.Dir = dir
	cmdLogger.Debug("Running command.")
	if out, err := cmd.CombinedOutput(); err != nil {
		cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
		return err
	}
	return nil
}
