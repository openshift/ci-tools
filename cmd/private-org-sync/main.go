package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	config.WhitelistOptions
	config.Options

	configDir string
	tokenPath string
	targetOrg string

	org  string
	repo string

	gitName  string
	gitEmail string

	gitDir               string
	confirm              bool
	failOnNonexistentDst bool
	debug                bool
}

func (o *options) validate() []error {
	var errs []error

	// TODO remove this after change the job to use the --config-dir arg
	if o.configDir != "" && o.ConfigDir == "" {
		o.ConfigDir = o.configDir
	}

	if err := o.Options.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("failed to validate config options: %w", err))
	}
	if err := o.Options.Complete(); err != nil {
		errs = append(errs, fmt.Errorf("failed to complete config options: %w", err))
	}

	if o.targetOrg == "" {
		errs = append(errs, fmt.Errorf("--target-org is required"))
	}
	if o.org != "" && o.targetOrg == o.org {
		errs = append(errs, fmt.Errorf("--only-org cannot be equal to --target-org"))
	}

	if o.repo != "" {
		if o.org != "" {
			errs = append(errs, fmt.Errorf("--only-org cannot be used together with --only-repo"))
		}
		items := strings.Split(o.repo, "/")
		if len(items) != 2 {
			errs = append(errs, fmt.Errorf("--only-repo must have org/repo format"))
		}
		if items[0] == o.targetOrg {
			errs = append(errs, fmt.Errorf("repo passed in --repo-only must have org different from --target-org"))
		}
	}

	if o.tokenPath == "" {
		errs = append(errs, fmt.Errorf("--token-path is required"))
	}

	if o.gitName == "" {
		errs = append(errs, fmt.Errorf("--git-name is not specified."))
	}

	if o.gitEmail == "" {
		errs = append(errs, fmt.Errorf("--git-email is not specified."))
	}

	if err := o.WhitelistOptions.Validate(); err != nil {
		errs = append(errs, err)

	}
	return errs
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.tokenPath, "token-path", "", "Path to token to use when pushing to GitHub.")
	fs.StringVar(&o.configDir, "config-path", "", "Path to directory containing ci-operator configurations")
	fs.StringVar(&o.targetOrg, "target-org", "", "Name of the org holding repos into which the git content should be mirrored")

	fs.StringVar(&o.org, "only-org", "", "Mirror only repos that belong to this org")
	fs.StringVar(&o.repo, "only-repo", "", "Mirror only a single repo")
	fs.StringVar(&o.gitDir, "git-dir", "", "Path to directory in which to perform Git operations")

	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git merge command.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git merge command.")

	fs.BoolVar(&o.confirm, "confirm", false, "Set true to actually execute all world-changing operations")
	fs.BoolVar(&o.failOnNonexistentDst, "fail-on-missing-destination", false, "Set true to make the tool to consider missing sync destination as an error")

	fs.BoolVar(&o.debug, "debug", false, "Set true to enable debug logging level")

	o.Options.Bind(fs)
	o.WhitelistOptions.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("Could not parse options")
	}
	return o
}

type gitFunc func(logger *logrus.Entry, dir string, command ...string) (string, int, error)

var second = time.Second

func withRetryOnNonzero(f gitFunc, retries int) gitFunc {
	return func(logger *logrus.Entry, dir string, command ...string) (string, int, error) {
		var out string
		var exitCode int
		var commandErr error
		err := wait.ExponentialBackoff(wait.Backoff{Duration: second, Factor: 2, Steps: retries}, func() (done bool, err error) {
			out, exitCode, commandErr = f(logger, dir, command...)
			return exitCode == 0, commandErr
		})
		return out, exitCode, err
	}
}

func gitExec(logger *logrus.Entry, dir string, command ...string) (string, int, error) {
	cmdLogger := logger.WithField("command", fmt.Sprintf("git %s", strings.Join(command, " ")))
	cmd := exec.Command("git", command...)
	cmd.Dir = dir
	cmdLogger.Debug("Running git")
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	var exitCode int
	if err != nil {
		errLogger := cmdLogger.WithError(err).WithField("output", out)
		if ee, ok := err.(*exec.ExitError); !ok {
			errLogger.Error("Failed to run git command")
		} else {
			exitCode = ee.ExitCode()
			errLogger.Debug("Git command was executed but completed with non-zero exit code")
			err = nil
		}
	} else {
		cmdLogger.WithField("output", out).Debug("Executed command")
	}

	return out, exitCode, err
}

type RemoteBranchHeads map[string]string

func getRemoteBranchHeads(logger *logrus.Entry, git gitFunc, repoDir, remote string) (RemoteBranchHeads, error) {
	heads := RemoteBranchHeads{}
	out, exitCode, err := git(logger, repoDir, "ls-remote", "--heads", remote)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("ls-remote failed: (exit code=%d output=%s)", exitCode, out)
	}

	out = strings.TrimSpace(out)
	if len(out) == 0 {
		return heads, nil
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("unexpected ls-remote output line: %s", line)
		}

		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		if len(branch) == len(fields[1]) {
			return nil, fmt.Errorf("unexpected ls-remote output line: %s", line)
		}
		heads[branch] = fields[0]
	}

	return heads, nil
}

// gitSyncer contains all code necessary to synchronize content from one GitHub
// location (org, repo, branch) to another
type gitSyncer struct {
	// GitHub token
	// Needs permissions to read the source and write to the destination
	token string
	// Path to a directory with local git repositories used for fetching and
	// pushing git content.
	root string
	// If false, no write operations will be done.
	confirm bool
	// If true, fail when destination repo does not exist
	failOnNonexistentDst bool

	logger *logrus.Entry

	gitName  string
	gitEmail string

	// wrapper for `git` execution: it is a member of the struct for testability
	git gitFunc
}

const fullFetch = 0
const startDepth = 1
const maxExpDepth = 6 // means we do at most `--depth=64`, then fallback to `--unshallow`
const unshallow = maxExpDepth + 1

func makeFetch(logger *logrus.Entry, repoDir string, git gitFunc, remote, branch string, expDepth int) func() error {
	return func() error {
		fetch := []string{"fetch", "--tags", remote, branch}

		depthArg := "full fetch" // no depth arg is used when doing a full fetch
		if expDepth != fullFetch {
			depthArg = "--unshallow"
			if expDepth < unshallow {
				depthArg = fmt.Sprintf("--depth=%d", int(math.Exp2(float64(expDepth))))
			}

			fetch = append(fetch, depthArg)
		}

		logger.Infof("Fetching from source (%s)", depthArg)
		if out, exitCode, err := git(logger, repoDir, fetch...); err != nil || exitCode != 0 {
			logger.WithError(err).WithField("exit-code", exitCode).WithField("output", out).Error("Failed to fetch from source")
			return fmt.Errorf("failed to fetch from source")
		}
		return nil
	}
}

func maybeTooShallow(pushOutput string) bool {
	patterns := []string{
		"shallow update not allowed",
		"Updates were rejected because the remote contains work that you do",
		"Updates were rejected because a pushed branch tip is behind its remote",
	}
	for _, item := range patterns {
		if strings.Contains(pushOutput, item) {
			return true
		}
	}
	return false
}

// location specifies a GitHub repository branch used as a source or destination
type location struct {
	org, repo, branch string
}

func (l location) String() string {
	return fmt.Sprintf("%s/%s@%s", l.org, l.repo, l.branch)
}

// makeGitDir creates a directory for a local git repo used for fetching content
// from the given location and pushing it to any other git repo
func (g gitSyncer) makeGitDir(org, repo string) (string, error) {
	repoDir := filepath.Join(g.root, org, repo)
	err := os.MkdirAll(repoDir, 0755)
	return repoDir, err
}

// mirror syncs content from source location to destination one, using a local
// repository in the given path. The `repoDir` directory must exist and be
// either empty, or previously used in a `mirror()` call. If it is empty,
// a git repository will be initialized in it. The git content from
// the `src` location will be fetched to this local repository and then
// pushed to the `dst` location. Multiple `mirror` calls over the same `repoDir`
// will reuse the content fetched in previous calls, acting like a cache.
func (g gitSyncer) mirror(repoDir string, src, dst location) error {
	mirrorFields := logrus.Fields{
		"source":      src.String(),
		"destination": dst.String(),
		"local-repo":  repoDir,
	}
	logger := g.logger.WithFields(mirrorFields)
	logger.Info("Syncing content between locations")

	// We ls-remote destination first thing because when it does not exist
	// we do not need to do any of the remaining operations.
	logger.Debug("Determining HEAD of destination branch")
	destUrlRaw := fmt.Sprintf("https://github.com/%s/%s", dst.org, dst.repo)
	destUrl, err := url.Parse(destUrlRaw)
	if err != nil {
		logger.WithField("remote-url", destUrlRaw).WithError(err).Error("Failed to construct URL for the destination remote")
		return fmt.Errorf("failed to construct URL for the destination remote")
	}
	destUrl.User = url.User(g.token)

	dstHeads, err := getRemoteBranchHeads(logger, g.git, repoDir, destUrl.String())
	if err != nil {
		message := "destination repository does not exist or we cannot access it"
		if g.failOnNonexistentDst {
			logger.Errorf(message)
			return fmt.Errorf(message)
		}

		logger.Warn(message)
		return nil
	}
	dstCommitHash := dstHeads[dst.branch]

	logger.Debug("Initializing git repository")
	if _, exitCode, err := g.git(logger, repoDir, "init"); err != nil || exitCode != 0 {
		logger.WithField("exit-code", exitCode).WithError(err).Error("Failed to initialize local git directory")
		return fmt.Errorf("failed to initialize local git directory")
	}

	// We set up a named remote for our source, called $org-$repo
	// We do this to allow git to reuse already fetched refs in subsequent fetches
	// so the local git repository acts like a cache.
	srcRemote := fmt.Sprintf("%s-%s", src.org, src.repo)
	_, exitCode, err := g.git(logger, repoDir, "remote", "get-url", srcRemote)
	if err != nil {
		logger.WithError(err).Error("Failed to query local git repository for remotes")
		return fmt.Errorf("failed to query local git repository for remotes")
	}

	if exitCode != 0 {
		if err := addGitRemote(logger, g.git, g.token, src.org, src.repo, repoDir, srcRemote); err != nil {
			return err
		}
	}

	logger.Debug("Determining HEAD of source branch")
	srcHeads, err := getRemoteBranchHeads(logger, withRetryOnNonzero(g.git, 5), repoDir, srcRemote)
	if err != nil {
		logger.WithError(err).Error("Failed to determine branch HEADs in source")
		return fmt.Errorf("failed to determine branch HEADs in source")
	}
	srcCommitHash, ok := srcHeads[src.branch]
	if !ok {
		logger.WithError(err).Error("Branch does not exist in source remote")
		return fmt.Errorf("branch does not exist in source remote")
	}

	if srcCommitHash == dstCommitHash {
		logger.Info("Branches are already in sync")
		return nil
	}

	depth := startDepth
	if len(dstHeads) == 0 {
		logger.Info("Destination is an empty repo: will do a full fetch right away")
		depth = fullFetch
	}

	push := func() (retry func() error, err error) {
		cmd := []string{"push", "--tags"}
		var logDryRun string
		if !g.confirm {
			cmd = append(cmd, "--dry-run")
			logDryRun = " (dry-run)"
		}
		cmd = append(cmd, destUrl.String(), fmt.Sprintf("FETCH_HEAD:refs/heads/%s", dst.branch))
		logger.Infof("Pushing to destination%s", logDryRun)

		out, exitCode, err := g.git(logger, repoDir, cmd...)
		if err == nil && exitCode == 0 {
			logger.Debug("Successfully pushed to destination")
			return nil, nil
		}

		if !maybeTooShallow(out) || err != nil {
			message := "failed to push to destination, no retry possible"
			logger.WithError(err).WithField("exit-code", exitCode).WithField("output", out).Error(message)
			return nil, fmt.Errorf(message)
		}

		if depth == unshallow {
			logger.Info("Trying to fetch source and destination full history and perform a merge")
			if err := mergeRemotesAndPush(logger, g.git, repoDir, srcRemote, dst.branch, destUrl.String(), g.confirm, g.gitName, g.gitEmail); err != nil {
				return nil, fmt.Errorf("failed to fetch remote and merge: %w", err)
			}
			return nil, nil
		}

		shallowOut, shallowExitCode, shallowErr := g.git(logger, repoDir, "rev-parse", "--is-shallow-repository")
		if shallowErr != nil || shallowExitCode != 0 {
			message := "failed to push to destination, no retry possible (cannot determine whether our git repo is shallow)"
			logger.WithError(shallowErr).WithField("exit-code", shallowExitCode).WithField("output", shallowOut).Error(message)
			return nil, fmt.Errorf(message)
		}

		switch strings.TrimSpace(shallowOut) {
		case "false":
			logger.Info("Trying to fetch source and destination full history and perform a merge")
			if err := mergeRemotesAndPush(logger, g.git, repoDir, srcRemote, dst.branch, destUrl.String(), g.confirm, g.gitName, g.gitEmail); err != nil {
				return nil, fmt.Errorf("failed to fetch remote and merge: %w", err)
			}
			return nil, nil
		case "true":
			depth++
			return makeFetch(logger, repoDir, g.git, srcRemote, src.branch, depth), nil
		default:
			message := "failed to push to destination, no retry possible (cannot determine whether our git repo is shallow)"
			logger.Error(message)
			logger.Error("`rev-parse --is-shallow-repo` likely not supported by used git")
			return nil, fmt.Errorf(message)
		}
	}

	fetch := makeFetch(logger, repoDir, g.git, srcRemote, src.branch, depth)
	for fetch != nil {
		err := fetch()
		if err != nil {
			return err
		}

		fetch, err = push()
		if err != nil {
			return err
		}
		if fetch != nil {
			logger.Info("failed to push to destination, retrying with deeper fetch")
		}
	}

	return nil
}

func addGitRemote(logger *logrus.Entry, git gitFunc, token, org, repo, repoDir, remoteName string) error {
	remoteSetupLogger := logger.WithField("remote-name", remoteName)
	remoteSetupLogger.Debug("Remote does not exist, setting up")

	srcURL, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", org, repo))
	if err != nil {
		remoteSetupLogger.WithError(err).Error("Failed to construct URL for the source remote")
		return fmt.Errorf("failed to construct URL for the source remote")
	}
	if token != "" {
		srcURL.User = url.User(token)
	}

	remoteSetupLogger = remoteSetupLogger.WithField("remote-url", srcURL.String())
	remoteSetupLogger.Debug("Adding remote")

	if _, exitCode, err := git(logger, repoDir, "remote", "add", remoteName, srcURL.String()); err != nil || exitCode != 0 {
		remoteSetupLogger.WithField("exit-code", exitCode).WithError(err).Error("Failed to set up source remote")
		return fmt.Errorf("failed to set up source remote")
	}

	return nil
}

func mergeRemotesAndPush(logger *logrus.Entry, git gitFunc, repoDir, srcRemote, branch, destURL string, confirm bool, gitName, gitEmail string) error {
	if err := checkGitError(git(logger, repoDir, []string{"fetch", destURL, branch}...)); err != nil {
		return fmt.Errorf("failed to fetch remote %s: %w", destURL, err)
	}

	if err := checkGitError(git(logger, repoDir, []string{"checkout", "FETCH_HEAD"}...)); err != nil {
		return fmt.Errorf("failed to checkout to FETCH_HEAD: %w", err)
	}

	sourceBranch := fmt.Sprintf("%s/%s", srcRemote, branch)
	if err := checkGitError(git(logger, repoDir, []string{"-c", fmt.Sprintf("user.name=%s", gitName), "-c", fmt.Sprintf("user.email=%s", gitEmail), "merge", sourceBranch, "-m", "DPTP reconciliation from upstream"}...)); err != nil {
		var mergeErrs []error
		mergeErrs = append(mergeErrs, fmt.Errorf("failed to merge %s: %w", sourceBranch, err))

		if err := checkGitError(git(logger, repoDir, []string{"merge", "--abort"}...)); err != nil {
			mergeErrs = append(mergeErrs, fmt.Errorf("failed to perform merge --abort: %w", err))
		}

		logger.WithError(utilerrors.NewAggregate(mergeErrs)).Warn("error occurred while fetching remote and merge")
		return nil
	}

	cmd := []string{"push", "--tags"}
	if !confirm {
		cmd = append(cmd, "--dry-run")
	}
	cmd = append(cmd, destURL, fmt.Sprintf("HEAD:%s", branch))

	err := checkGitError(git(logger, repoDir, cmd...))
	if err != nil {
		return fmt.Errorf("failed to push to destination: %w", err)
	}

	logger.Info("Successfully pushed to destination")
	return nil
}

func checkGitError(out string, exitCode int, err error) error {
	if err != nil {
		return err
	}

	if exitCode != 0 {
		return fmt.Errorf("failed with %d exit-code: %s", exitCode, out)
	}

	return nil
}

// makeFilter creates a callback usable for OperateOnCIOperatorConfigDir that
// only calls the original callback on files matching the business rules and
// options passed to the program
func (o *options) makeFilter(callback func(*api.ReleaseBuildConfiguration, *config.Info) error) func(*api.ReleaseBuildConfiguration, *config.Info) error {
	return func(c *api.ReleaseBuildConfiguration, i *config.Info) error {
		if o.org != "" && o.org != i.Org {
			return nil
		}
		if o.repo != "" && o.repo != fmt.Sprintf("%s/%s", i.Org, i.Repo) {
			return nil
		}
		if !promotion.BuildsOfficialImages(c, promotion.WithoutOKD) {
			return nil
		}
		return callback(c, i)
	}
}

func main() {
	o := gatherOptions()
	if errs := o.validate(); len(errs) > 0 {
		for _, err := range errs {
			logrus.WithError(err).Error("Invalid option")
		}
		logrus.Fatal("Invalid options, exiting")
	}

	if o.debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	var token string
	if rawToken, err := ioutil.ReadFile(o.tokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to read GitHub token")
	} else {
		token = strings.TrimSpace(string(rawToken))
		getter := func() sets.String {
			return sets.NewString(token)
		}
		logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, getter))
	}

	if o.gitDir == "" {
		var err error
		if o.gitDir, err = ioutil.TempDir("", ""); err != nil {
			logrus.WithError(err).Fatal("Failed to create temporary directory for git operations")
		}
		defer func() {
			if err := os.RemoveAll(o.gitDir); err != nil {
				logrus.WithError(err).Fatal("Failed to clean up temporary directory for git operations")
			}
		}()
	}

	syncer := gitSyncer{
		token:                token,
		root:                 o.gitDir,
		confirm:              o.confirm,
		git:                  gitExec,
		failOnNonexistentDst: o.failOnNonexistentDst,
		gitName:              o.gitName,
		gitEmail:             o.gitEmail,
	}

	var errs []error

	locations, whitelistErrors := getWhitelistedLocations(o.WhitelistOptions.WhitelistConfig.Whitelist, syncer.git, token)
	errs = append(errs, whitelistErrors...)

	callback := func(_ *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		l := location{
			org:    repoInfo.Org,
			repo:   repoInfo.Repo,
			branch: repoInfo.Branch,
		}
		locations[l] = struct{}{}
		return nil
	}

	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, o.makeFilter(callback)); err != nil {
		errs = append(errs, err)
	}

	for source := range locations {
		syncer.logger = config.LoggerForInfo(config.Info{
			Metadata: api.Metadata{
				Org:    source.org,
				Repo:   source.repo,
				Branch: source.branch,
			},
		})

		destination := source
		destination.org = o.targetOrg
		gitDir, err := syncer.makeGitDir(source.org, source.repo)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s->%s: %w", source.String(), destination.String(), err))
			continue
		}

		if err := syncer.mirror(gitDir, source, destination); err != nil {
			errs = append(errs, fmt.Errorf("%s->%s: %w", source.String(), destination.String(), err))
		}
	}

	if len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatal("There were failures")
	}
}

func getWhitelistedLocations(whitelist map[string][]string, git gitFunc, token string) (map[location]struct{}, []error) {
	var errs []error
	locations := make(map[location]struct{})

	for org, repos := range whitelist {
		for _, repo := range repos {
			remoteURL, err := url.Parse(fmt.Sprintf("https://github.com/%s/%s", org, repo))
			if err != nil {
				logrus.WithError(err).Error("Failed to construct URL for the remote")
				if err != nil {
					errs = append(errs, err)
					continue
				}
			}
			if token != "" {
				remoteURL.User = url.User(token)
			}
			logger := logrus.WithField("remote", remoteURL.String())
			branches, err := getRemoteBranchHeads(logger, git, "", remoteURL.String())
			if err != nil {
				errs = append(errs, err)
				continue
			}

			for branch := range branches {
				locations[location{org: org, repo: repo, branch: branch}] = struct{}{}
			}
		}
	}
	return locations, errs
}
