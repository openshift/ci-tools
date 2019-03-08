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

	"github.com/openshift/ci-operator/pkg/api"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator-prowgen/pkg/promotion"
)

type options struct {
	promotion.Options
	gitDir      string
	username    string
	tokenPath   string
	fastForward bool
}

func (o *options) Validate() error {
	if err := o.Options.Validate(); err != nil {
		return err
	}
	if o.Confirm {
		if o.username == "" {
			return errors.New("--username is required with --confirm")
		}
		if o.tokenPath == "" {
			return errors.New("--token-path is required with --confirm")
		}
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.gitDir, "git-dir", "", "Optional dir to do git operations in. If unset, temp dir will be used.")
	fs.StringVar(&o.username, "username", "", "Username to use when pushing to GitHub.")
	fs.StringVar(&o.tokenPath, "token-path", "", "Path to token to use when pushing to GitHub.")
	fs.BoolVar(&o.fastForward, "fast-forward", false, "Attempt to fast-forward future branches if they already exist.")
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

type censoringFormatter struct {
	secret   string
	delegate logrus.Formatter
}

func (f *censoringFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	for key, value := range entry.Data {
		if valueString, ok := value.(string); ok {
			if strings.Contains(valueString, f.secret) {
				entry.Data[key] = strings.Replace(valueString, f.secret, "xxx", -1)
			}
		}
	}
	return f.delegate.Format(entry)
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
			if err := os.RemoveAll(tempDir); err != nil {
				logrus.WithError(err).Fatal("Could not clean up temp dir for git operations")
			}
		}()
		gitDir = tempDir
	}

	var token string
	if o.Confirm {
		if rawToken, err := ioutil.ReadFile(o.tokenPath); err != nil {
			logrus.WithError(err).Fatal("Could not read token.")
		} else {
			token = string(rawToken)
			logrus.SetFormatter(&censoringFormatter{delegate: new(logrus.TextFormatter), secret: token})
		}
	}

	if err := config.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := config.LoggerForInfo(*repoInfo)
		if (o.Org != "" && o.Org != repoInfo.Org) || (o.Repo != "" && o.Repo != repoInfo.Repo) {
			return nil
		}
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
			remote.User = url.UserPassword(o.username, token)
		}
		for _, command := range [][]string{{"init"}, {"fetch", "--depth", "10", remote.String(), repoInfo.Branch}} {
			cmdLogger := logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
			cmd := exec.Command("git", command...)
			cmd.Dir = repoDir
			cmdLogger.Debug("Running command.")
			if out, err := cmd.CombinedOutput(); err != nil {
				cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
				return nil
			} else {
				cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
			}
		}

		// when we're initializing the branch, we just want to make sure
		// it is in sync with the current branch that is promoting
		for _, futureBranch := range []string{futureBranchForCurrentPromotion, futureBranchForFuturePromotion} {
			if futureBranch == repoInfo.Branch {
				continue
			}
			branchLogger := logger.WithField("future-branch", futureBranch)
			command := []string{"ls-remote", remote.String(), fmt.Sprintf("refs/heads/%s", futureBranch)}
			cmdLogger := branchLogger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
			cmd := exec.Command("git", command...)
			cmd.Dir = repoDir
			cmdLogger.Debug("Running command.")
			if out, err := cmd.CombinedOutput(); err != nil {
				cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
				continue
			} else {
				cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
				if string(out) == "" && !o.fastForward {
					branchLogger.Info("Remote already has branch, skipping.")
					continue
				}
			}

			if !o.Confirm {
				branchLogger.Info("Would create new branch.")
				continue
			}

			command = []string{"push", remote.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", futureBranch)}
			cmdLogger = branchLogger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
			cmd = exec.Command("git", command...)
			cmd.Dir = repoDir
			cmdLogger.Debug("Running command.")
			if out, err := cmd.CombinedOutput(); err != nil {
				fastForwardErr := strings.Contains(err.Error(), "Updates were rejected because a pushed branch tip is behind its remote")
				if !fastForwardErr || (fastForwardErr && !o.fastForward) {
					cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
					return nil
				}
			} else {
				cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
				branchLogger.Info("Pushed new branch.")
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}
}
