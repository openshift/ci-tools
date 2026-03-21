package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	promotion.FutureOptions
	gitDir    string
	username  string
	tokenPath string
	ignore    flagutil.Strings
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
	fs.Var(&o.ignore, "ignore", "Ignore a repo or entire org. Format: org or org/repo. Can be passed multiple times.")
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

type gitCmd func(l *logrus.Entry, args ...string) error

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	ignoreSet := o.ignore.StringSet()

	gitDir := o.gitDir
	if gitDir == "" {
		tempDir, err := os.MkdirTemp("", "")
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
		if rawToken, err := os.ReadFile(o.tokenPath); err != nil {
			logrus.WithError(err).Fatal("Could not read token.")
		} else {
			token = strings.TrimSpace(string(rawToken))
			logrus.SetFormatter(&censoringFormatter{delegate: new(logrus.TextFormatter), secret: token})
		}
	}

	brachingFailure := false
	failedConfigs := sets.New[string]()
	appendFailedConfig := func(c *api.ReleaseBuildConfiguration) {
		configInfo := fmt.Sprintf("%s/%s@%s", c.Metadata.Org, c.Metadata.Repo, c.Metadata.Branch)
		if c.Metadata.Variant != "" {
			configInfo += "__" + c.Metadata.Variant
		}
		failedConfigs.Insert(configInfo)
	}

	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, api.WithoutOKD, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		if ignoreSet.Has(repoInfo.Org) || ignoreSet.Has(fmt.Sprintf("%s/%s", repoInfo.Org, repoInfo.Repo)) {
			logrus.WithField("repo", fmt.Sprintf("%s/%s", repoInfo.Org, repoInfo.Repo)).Info("Skipping due to --ignore")
			return nil
		}

		logger := config.LoggerForInfo(*repoInfo)

		remote, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", repoInfo.Org, repoInfo.Repo))
		if err != nil {
			logger.WithError(err).Error("Could not construct remote URL.")
			appendFailedConfig(configuration)
			return err
		}
		if o.Confirm {
			remote.User = url.UserPassword(o.username, token)
		}

		// Determine which future branches need work by comparing remote refs
		// in a single ls-remote call instead of fetching and pushing each one.
		remoteRefs, err := lsRemoteRefs(logger, remote)
		if err != nil {
			appendFailedConfig(configuration)
			return err
		}
		currentSHA := remoteRefs[fmt.Sprintf("refs/heads/%s", repoInfo.Branch)]
		if currentSHA == "" {
			logger.Error("Current branch not found on remote.")
			appendFailedConfig(configuration)
			return nil
		}

		var branchesNeedingWork []string
		for _, futureRelease := range o.FutureReleases.Strings() {
			futureBranch, err := promotion.DetermineReleaseBranch(o.CurrentRelease, futureRelease, repoInfo.Branch)
			if err != nil {
				logger.WithError(err).Error("could not determine release branch")
				appendFailedConfig(configuration)
				return nil
			}
			if futureBranch == repoInfo.Branch {
				continue
			}

			futureLogger := logger.WithField("future-branch", futureBranch)
			futureRef := fmt.Sprintf("refs/heads/%s", futureBranch)
			if remoteRefs[futureRef] == currentSHA {
				futureLogger.Debug("Already up to date, skipping.")
				continue
			}

			if !o.Confirm {
				futureLogger.Info("Would create new branch.")
				continue
			}

			branchesNeedingWork = append(branchesNeedingWork, futureBranch)
		}

		if !o.Confirm || len(branchesNeedingWork) == 0 {
			return nil
		}

		// Only init and fetch if there is actual work to do.
		repoDir := path.Join(gitDir, repoInfo.Org, repoInfo.Repo)
		if err := os.MkdirAll(repoDir, 0775); err != nil {
			logger.WithError(err).Fatal("could not ensure git dir existed")
			return nil
		}

		gitCmd := gitCmdFunc(repoDir)
		for _, command := range [][]string{{"init"}, {"fetch", "--depth", "1", remote.String(), repoInfo.Branch}} {
			if err := gitCmd(logger, command...); err != nil {
				appendFailedConfig(configuration)
				return err
			}
		}

		for _, futureBranch := range branchesNeedingWork {
			futureLogger := logger.WithField("future-branch", futureBranch)

			for depth := 1; depth <= 9; depth += 1 {
				retry, err := pushBranch(futureLogger, remote, futureBranch, gitCmd)
				if err != nil {
					futureLogger.WithError(err).Error("Failed to push branch")
					appendFailedConfig(configuration)
					break
				}

				if !retry {
					break
				}

				if depth == 9 {
					futureLogger.Error("Could not push branch even after unshallowing.")
					appendFailedConfig(configuration)
					break
				}

				if depth == 8 {
					futureLogger.Warn("Progressive deepening was not enough, fetching full history...")
					if err := fetchUnshallow(futureLogger, remote, gitCmd, repoInfo); err != nil {
						appendFailedConfig(configuration)
						return nil
					}
				} else if err := fetchDeeper(futureLogger, remote, gitCmd, repoInfo, int(math.Exp2(float64(depth-1)))); err != nil {
					appendFailedConfig(configuration)
					return nil
				}
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Error("Could not branch configurations.")
		brachingFailure = true
	}

	if len(failedConfigs) > 0 {
		logrus.WithField("configs", failedConfigs.UnsortedList()).Error("Failed configurations.")
		brachingFailure = true
	}

	if brachingFailure {
		os.Exit(1)
	}
}

// lsRemoteRefs runs git ls-remote and returns a map of ref -> SHA.
// This does not require a local repo — git ls-remote works without one.
func lsRemoteRefs(logger *logrus.Entry, remote *url.URL) (map[string]string, error) {
	refs := map[string]string{}
	var b []byte
	var err error
	sleepyTime := time.Second
	for i := 0; i < 3; i++ {
		c := exec.Command("git", "ls-remote", remote.String())
		b, err = c.CombinedOutput()
		if err == nil {
			break
		}
		logger.WithError(err).Debugf("ls-remote failed (attempt %d/3), retrying...", i+1)
		time.Sleep(sleepyTime)
		sleepyTime *= 2
	}
	if err != nil {
		logger.WithError(err).Error("Failed to run ls-remote after retries.")
		return nil, fmt.Errorf("ls-remote failed: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			refs[parts[1]] = parts[0]
		}
	}
	return refs, nil
}

func pushBranch(logger *logrus.Entry, remote *url.URL, futureBranch string, gitCmd gitCmd) (bool, error) {
	command := []string{"push", remote.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", futureBranch)}
	logger = logger.WithFields(logrus.Fields{"commands": fmt.Sprintf("git %s", strings.Join(command, " "))})
	if err := gitCmd(logger, command...); err != nil {
		tooShallowErr := strings.Contains(err.Error(), "Updates were rejected because the remote contains work that you do")
		if tooShallowErr {
			logger.Warn("Failed to push, trying a deeper clone...")
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func fetchDeeper(logger *logrus.Entry, remote *url.URL, gitCmd gitCmd, repoInfo *config.Info, deepenBy int) error {
	command := []string{"fetch", "--deepen", strconv.Itoa(deepenBy), remote.String(), repoInfo.Branch}
	if err := gitCmd(logger, command...); err != nil {
		return err
	}
	return nil
}

func fetchUnshallow(logger *logrus.Entry, remote *url.URL, gitCmd gitCmd, repoInfo *config.Info) error {
	command := []string{"fetch", "--unshallow", remote.String(), repoInfo.Branch}
	if err := gitCmd(logger, command...); err != nil {
		return err
	}
	return nil
}

func gitCmdFunc(dir string) gitCmd {
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
