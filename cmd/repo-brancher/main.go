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
	"time"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	promotion.FutureOptions
	gitDir      string
	username    string
	tokenPath   string
	fastForward bool
}

func (o *options) Validate() error {
	if err := o.FutureOptions.Validate(); err != nil {
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

func (o *options) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.gitDir, "git-dir", "", "Optional dir to do git operations in. If unset, temp dir will be used.")
	fs.StringVar(&o.username, "username", "", "Username to use when pushing to GitHub.")
	fs.StringVar(&o.tokenPath, "token-path", "", "Path to token to use when pushing to GitHub.")
	fs.BoolVar(&o.fastForward, "fast-forward", false, "Attempt to fast-forward future branches if they already exist.")
	o.FutureOptions.Bind(fs)
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.bind(fs)
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
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, promotion.WithoutOKD, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := config.LoggerForInfo(*repoInfo)

		repoDir := path.Join(gitDir, repoInfo.Org, repoInfo.Repo)
		if err := os.MkdirAll(repoDir, 0775); err != nil {
			logger.WithError(err).Fatal("could not ensure git dir existed")
			return nil
		}

		executeGitCmd := executeGitCMDFactory(repoDir)

		remote, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", repoInfo.Org, repoInfo.Repo))
		if err != nil {
			logger.WithError(err).Fatal("Could not construct remote URL.")
		}
		if o.Confirm {
			remote.User = url.UserPassword(o.username, token)
		}
		for _, command := range [][]string{{"init"}, {"fetch", "--depth", "1", remote.String(), repoInfo.Branch}} {
			if err := executeGitCmd(logger, command...); err != nil {
				failed = true
				return nil
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
			logger := logger.WithField("future-branch", futureBranch)
			command := []string{"ls-remote", remote.String(), fmt.Sprintf("refs/heads/%s", futureBranch)}
			if err := executeGitCmd(logger, command...); err != nil {
				failed = true
				continue
			}

			if !o.Confirm {
				logger.Info("Would create new branch.")
				continue
			}

			pushBranch := func() (retry bool) {
				command = []string{"push", remote.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", futureBranch)}
				logger := logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
				if err := executeGitCmd(logger, command...); err != nil {
					tooShallowErr := strings.Contains(err.Error(), "Updates were rejected because the remote contains work that you do")
					if tooShallowErr {
						logger.Warn("Failed to push, trying a deeper clone...")
						return true
					}
					failed = true
				}
				return false
			}

			fetchDeeper := func(depth int) error {
				command = []string{"fetch", "--depth", strconv.Itoa(depth), remote.String(), repoInfo.Branch}
				if err := executeGitCmd(logger, command...); err != nil {
					failed = true
					return err
				}
				return nil
			}

			for depth := 1; depth < 9; depth += 1 {
				retry := pushBranch()
				if !retry {
					break
				}

				if depth == 8 && retry {
					logger.Error("Could not push branch even with retries.")
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

func executeGitCMDFactory(dir string) func(l *logrus.Entry, args ...string) error {
	return func(l *logrus.Entry, args ...string) error {
		l = l.WithField("commands", fmt.Sprintf("git %s", strings.Join(args, " ")))
		var b []byte
		var err error
		l.Debug("Running command.")
		sleepyTime := time.Second
		for i := 0; i < 3; i++ {
			c := exec.Command("git", args...)
			c.Dir = dir
			b, err = c.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("running git %v returned error %w with output %q", args, err, string(b))
				l.WithError(err).Debugf("Retrying #%d, if this is not the 3rd try then this will be retried", i+1)
				time.Sleep(sleepyTime)
				sleepyTime *= 2
				continue
			}
			break
		}
		l = l.WithField("output", string(b))
		if err != nil {
			l.Error("Failed to execute command.")
			return err
		}

		l.Debug("Executed command.")
		return nil
	}
}
