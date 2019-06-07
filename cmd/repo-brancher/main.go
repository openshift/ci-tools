package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
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
			token = strings.TrimSpace(string(rawToken))
			logrus.SetFormatter(&censoringFormatter{delegate: new(logrus.TextFormatter), secret: token})
		}
	}

	failed := false
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

		remote, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", repoInfo.Org, repoInfo.Repo))
		if err != nil {
			logger.WithError(err).Fatal("Could not construct remote URL.")
		}
		if o.Confirm {
			remote.User = url.UserPassword(o.username, token)
		}
		for _, command := range [][]string{{"init"}, {"fetch", "--depth", "1", remote.String(), repoInfo.Branch}} {
			cmdLogger := logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
			cmd := exec.Command("git", command...)
			cmd.Dir = repoDir
			cmdLogger.Debug("Running command.")
			if out, err := cmd.CombinedOutput(); err != nil {
				cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
				failed = true
				return nil
			} else {
				cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
			}
		}

		for _, futureRelease := range o.FutureReleases.Strings() {
			futureBranch, err := promotion.DetermineReleaseBranch(o.CurrentRelease, futureRelease, repoInfo.Branch)
			if err != nil {
				logger.WithError(err).Error("could not determine release branch")
				failed = true
				return nil
			}
			if futureBranch == repoInfo.Branch {
				continue
			}

			// when we're initializing the branch, we just want to make sure
			// it is in sync with the current branch that is promoting
			branchLogger := logger.WithField("future-branch", futureBranch)
			command := []string{"ls-remote", remote.String(), fmt.Sprintf("refs/heads/%s", futureBranch)}
			cmdLogger := branchLogger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
			cmd := exec.Command("git", command...)
			cmd.Dir = repoDir
			cmdLogger.Debug("Running command.")
			if out, err := cmd.CombinedOutput(); err != nil {
				cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
				failed = true
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

			pushBranch := func() (retry bool) {
				command = []string{"push", remote.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", futureBranch)}
				cmdLogger = branchLogger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
				cmd = exec.Command("git", command...)
				cmd.Dir = repoDir
				cmdLogger.Debug("Running command.")
				if out, err := cmd.CombinedOutput(); err != nil {
					errLogger := cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)})
					tooShallowErr := strings.Contains(string(out), "Updates were rejected because the remote contains work that you do")
					if tooShallowErr {
						errLogger.Warn("Failed to push, trying a deeper clone...")
						return true
					}
					errLogger.Error("Failed to execute command.")
					failed = true
					return false
				} else {
					cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
					branchLogger.Info("Pushed new branch.")
					return false
				}
			}

			fetchDeeper := func(depth int) error {
				command = []string{"fetch", "--depth", strconv.Itoa(depth), remote.String(), repoInfo.Branch}
				cmdLogger := logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
				cmd := exec.Command("git", command...)
				cmd.Dir = repoDir
				cmdLogger.Debug("Running command.")
				if out, err := cmd.CombinedOutput(); err != nil {
					cmdLogger.WithError(err).WithFields(logrus.Fields{"output": string(out)}).Error("Failed to execute command.")
					failed = true
					return err
				} else {
					cmdLogger.WithFields(logrus.Fields{"output": string(out)}).Debug("Executed command.")
					return nil
				}
			}

			for depth := 1; depth < 9; depth += 1 {
				retry := pushBranch()
				if !retry {
					break
				}

				if depth == 8 && retry {
					branchLogger.Error("Could not push branch even with retries.")
					failed = true
					break
				}

				if err := fetchDeeper(int(math.Exp2(float64(depth)))); err != nil {
					break
				}
			}
		}
		return nil
	}); err != nil || failed {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}
}
