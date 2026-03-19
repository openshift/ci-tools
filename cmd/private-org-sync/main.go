package main

import (
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

// defaultFlattenOrgs contains organizations whose repos should not have org prefix by default
// for backwards compatibility
var defaultFlattenOrgs = []string{
	"openshift",
	"openshift-eng",
	"operator-framework",
	"redhat-cne",
	"openshift-assisted",
	"ViaQ",
}

type arrayFlags []string

func (i *arrayFlags) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type options struct {
	config.WhitelistOptions
	config.Options

	configDir string
	tokenPath string
	targetOrg string

	prefix      string
	org         string
	repo        string
	flattenOrgs arrayFlags

	gitName  string
	gitEmail string

	gitDir               string
	confirm              bool
	failOnNonexistentDst bool
	debug                bool
	parallelism          int
}

const defaultPrefix = "https://github.com"

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
		errs = append(errs, fmt.Errorf("--git-name is not specified"))
	}

	if o.gitEmail == "" {
		errs = append(errs, fmt.Errorf("--git-email is not specified"))
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

	fs.StringVar(&o.prefix, "prefix", defaultPrefix, "Prefix used for all git URLs")
	fs.StringVar(&o.org, "only-org", "", "Mirror only repos that belong to this org")
	fs.StringVar(&o.repo, "only-repo", "", "Mirror only a single repo")
	fs.Var(&o.flattenOrgs, "flatten-org", "Organizations whose repos should not have org prefix (can be specified multiple times)")
	fs.StringVar(&o.gitDir, "git-dir", "", "Path to directory in which to perform Git operations")

	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git merge command.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git merge command.")

	fs.BoolVar(&o.confirm, "confirm", false, "Set true to actually execute all world-changing operations")
	fs.BoolVar(&o.failOnNonexistentDst, "fail-on-missing-destination", false, "Set true to make the tool to consider missing sync destination as an error")

	fs.BoolVar(&o.debug, "debug", false, "Set true to enable debug logging level")
	fs.IntVar(&o.parallelism, "parallelism", 4, "Number of repos to sync in parallel")

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

func withRetryOnTransientError(f gitFunc, retries int) gitFunc {
	return func(logger *logrus.Entry, dir string, command ...string) (string, int, error) {
		var out string
		var exitCode int
		var commandErr error
		for attempt := 1; attempt <= retries; attempt++ {
			out, exitCode, commandErr = f(logger, dir, command...)
			if commandErr == nil && exitCode == 0 {
				return out, exitCode, nil
			}
			if attempt < retries && isTransientNetworkError(out) {
				logger.Infof("Transient network error, retrying git command (%d/%d)", attempt, retries)
				time.Sleep(5 * time.Second)
				continue
			}
			break
		}
		return out, exitCode, commandErr
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
	// Prefix used for all URLs.
	// Points to GitHub by default, but can be used to point somewhere else.
	prefix string
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
const maxExpDepth = 6 // means we deepen at most to 64 commits total, then fallback to `--unshallow`
const unshallow = maxExpDepth + 1

type fetchOptions struct {
	withTags  bool
	useDeepen bool
}

func makeFetch(logger *logrus.Entry, repoDir string, git gitFunc, remote, branch string, expDepth int, opts fetchOptions) func() error {
	return func() error {
		fetch := []string{"fetch"}
		if opts.withTags {
			fetch = append(fetch, "--tags")
		}
		fetch = append(fetch, remote, branch)

		depthArg := "full fetch" // no depth arg is used when doing a full fetch
		if expDepth != fullFetch {
			depthArg = "--unshallow"
			if expDepth < unshallow {
				if opts.useDeepen {
					deepenBy := int(math.Exp2(float64(expDepth - 1)))
					depthArg = fmt.Sprintf("--deepen=%d", deepenBy)
				} else {
					depthArg = fmt.Sprintf("--depth=%d", int(math.Exp2(float64(expDepth))))
				}
			}

			fetch = append(fetch, depthArg)
		}

		logger.Infof("Fetching from source (%s)", depthArg)
		const shallowRetries = 3
		for attempt := 1; attempt <= shallowRetries; attempt++ {
			out, exitCode, err := git(logger, repoDir, fetch...)
			if err == nil && exitCode == 0 {
				return nil
			}
			if attempt < shallowRetries && strings.Contains(out, "shallow file has changed since we read it") {
				logger.Infof("Shallow file changed during fetch, retrying (%d/%d)", attempt, shallowRetries)
				continue
			}
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
		"remote unpack failed: index-pack failed",
	}
	for _, item := range patterns {
		if strings.Contains(pushOutput, item) {
			return true
		}
	}
	return false
}

func isTransientNetworkError(output string) bool {
	patterns := []string{
		"Could not resolve host",
		"Failed to connect to",
		"Connection timed out",
		"Connection refused",
		"Connection reset by peer",
		"The requested URL returned error: 5",
	}
	for _, pattern := range patterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

// location specifies a GitHub repository branch used as a source or destination
type location struct {
	org, repo, branch string
}

type repoKey struct{ org, repo string }

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

// initRepo initializes a local git repository and sets up the source remote.
// This should be called once per (org, repo) pair before calling mirror() for
// individual branches.
func (g gitSyncer) initRepo(repoDir, org, repo string) error {
	logger := g.logger

	logger.Debug("Initializing git repository")
	if _, exitCode, err := g.git(logger, repoDir, "init"); err != nil || exitCode != 0 {
		logger.WithField("exit-code", exitCode).WithError(err).Error("Failed to initialize local git directory")
		return fmt.Errorf("failed to initialize local git directory")
	}

	srcRemote := fmt.Sprintf("%s-%s", org, repo)
	_, exitCode, err := g.git(logger, repoDir, "remote", "get-url", srcRemote)
	if err != nil {
		logger.WithError(err).Error("Failed to query local git repository for remotes")
		return fmt.Errorf("failed to query local git repository for remotes")
	}

	if exitCode != 0 {
		if err := addGitRemote(logger, g.git, g.prefix, g.token, org, repo, repoDir, srcRemote); err != nil {
			return err
		}
	}

	return nil
}

// syncRepo initializes a local git repo, fetches branch heads from both source
// and destination via ls-remote, and mirrors each branch that needs syncing.
func (g gitSyncer) syncRepo(org, repo, targetOrg, dstRepo string, branches []location) []error {
	var errs []error
	repoLogger := g.logger

	gitDir, err := g.makeGitDir(org, repo)
	if err != nil {
		for _, source := range branches {
			errs = append(errs, fmt.Errorf("%s: %w", source.String(), err))
		}
		return errs
	}

	if err := g.initRepo(gitDir, org, repo); err != nil {
		for _, source := range branches {
			errs = append(errs, fmt.Errorf("%s: %w", source.String(), err))
		}
		return errs
	}

	// ls-remote source and destination in parallel
	destUrlRaw := fmt.Sprintf("%s/%s/%s", g.prefix, targetOrg, dstRepo)
	destUrl, err := url.Parse(destUrlRaw)
	if err != nil {
		repoLogger.WithField("remote-url", destUrlRaw).WithError(err).Error("Failed to construct URL for the destination remote")
		for _, source := range branches {
			errs = append(errs, fmt.Errorf("%s: failed to construct URL for the destination remote", source.String()))
		}
		return errs
	}
	if g.token != "" {
		destUrl.User = url.User(g.token)
	}

	srcRemote := fmt.Sprintf("%s-%s", org, repo)

	type lsRemoteResult struct {
		heads RemoteBranchHeads
		err   error
	}
	dstResult := make(chan lsRemoteResult, 1)
	srcResult := make(chan lsRemoteResult, 1)
	go func() {
		heads, err := getRemoteBranchHeads(repoLogger, g.git, gitDir, destUrl.String())
		dstResult <- lsRemoteResult{heads, err}
	}()
	go func() {
		heads, err := getRemoteBranchHeads(repoLogger, withRetryOnNonzero(g.git, 5), gitDir, srcRemote)
		srcResult <- lsRemoteResult{heads, err}
	}()

	dst := <-dstResult
	src := <-srcResult

	if dst.err != nil {
		message := "destination repository does not exist or we cannot access it"
		if g.failOnNonexistentDst {
			repoLogger.Errorf("%s", message)
			for _, source := range branches {
				errs = append(errs, fmt.Errorf("%s: %s", source.String(), message))
			}
		} else {
			repoLogger.Warn(message)
		}
		return errs
	}

	if src.err != nil {
		repoLogger.WithError(src.err).Error("Failed to determine branch HEADs in source")
		for _, source := range branches {
			errs = append(errs, fmt.Errorf("%s: failed to determine branch HEADs in source", source.String()))
		}
		return errs
	}

	dstHeads := dst.heads
	srcHeads := src.heads

	for _, source := range branches {
		g.logger = config.LoggerForInfo(config.Info{
			Metadata: api.Metadata{
				Org:    source.org,
				Repo:   source.repo,
				Branch: source.branch,
			},
		})

		destination := location{org: targetOrg, repo: dstRepo, branch: source.branch}

		if err := g.mirror(gitDir, source, destination, srcHeads, dstHeads, destUrl); err != nil {
			errs = append(errs, fmt.Errorf("%s->%s: %w", source.String(), destination.String(), err))
		}
	}

	return errs
}

// mirror syncs a single branch from source to destination, using pre-fetched
// branch head information. The `repoDir` must have been previously initialized
// with git init and remote setup. The `srcHeads` and `dstHeads` must have been
// obtained from ls-remote calls against the source and destination repos.
// Multiple `mirror` calls over the same `repoDir` will reuse the content
// fetched in previous calls, acting like a cache.
func (g gitSyncer) mirror(repoDir string, src, dst location, srcHeads, dstHeads RemoteBranchHeads, destUrl *url.URL) error {
	mirrorFields := logrus.Fields{
		"source":      src.String(),
		"destination": dst.String(),
		"local-repo":  repoDir,
	}
	logger := g.logger.WithFields(mirrorFields)
	logger.Info("Syncing content between locations")

	dstCommitHash := dstHeads[dst.branch]

	srcRemote := fmt.Sprintf("%s-%s", src.org, src.repo)

	srcCommitHash, ok := srcHeads[src.branch]
	if !ok {
		logger.Error("Branch does not exist in source remote")
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
			return nil, fmt.Errorf("%s", message)
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
			return nil, fmt.Errorf("%s", message)
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
			return makeFetch(logger, repoDir, g.git, srcRemote, src.branch, depth, fetchOptions{useDeepen: true}), nil
		default:
			message := "failed to push to destination, no retry possible (cannot determine whether our git repo is shallow)"
			logger.Error(message)
			logger.Error("`rev-parse --is-shallow-repository` likely not supported by used git")
			return nil, fmt.Errorf("%s", message)
		}
	}

	fetch := makeFetch(logger, repoDir, g.git, srcRemote, src.branch, depth, fetchOptions{withTags: true})
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

func addGitRemote(logger *logrus.Entry, git gitFunc, prefix, token, org, repo, repoDir, remoteName string) error {
	remoteSetupLogger := logger.WithField("remote-name", remoteName)
	remoteSetupLogger.Debug("Remote does not exist, setting up")

	srcURL, err := url.Parse(fmt.Sprintf("%s/%s/%s", prefix, org, repo))
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
		if !api.BuildsAnyOfficialImages(c, api.WithoutOKD) {
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
	if rawToken, err := os.ReadFile(o.tokenPath); err != nil {
		logrus.WithError(err).Fatal("Failed to read GitHub token")
	} else {
		token = strings.TrimSpace(string(rawToken))
		getter := func() sets.Set[string] {
			return sets.New[string](token)
		}
		logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, getter))
	}

	if o.gitDir == "" {
		var err error
		if o.gitDir, err = os.MkdirTemp("", ""); err != nil {
			logrus.WithError(err).Fatal("Failed to create temporary directory for git operations")
		}
		defer func() {
			if err := os.RemoveAll(o.gitDir); err != nil {
				logrus.WithError(err).Fatal("Failed to clean up temporary directory for git operations")
			}
		}()
	}

	syncer := gitSyncer{
		prefix:               o.prefix,
		token:                token,
		root:                 o.gitDir,
		confirm:              o.confirm,
		git:                  withRetryOnTransientError(gitExec, 3),
		failOnNonexistentDst: o.failOnNonexistentDst,
		gitName:              o.gitName,
		gitEmail:             o.gitEmail,
	}

	var errs []error

	locations, whitelistErrors := getWhitelistedLocations(o.WhitelistOptions.WhitelistConfig.Whitelist, syncer.git, o.prefix, token)
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

	// Group locations by (org, repo) so we can initialize each repo once
	grouped := make(map[repoKey][]location)
	for source := range locations {
		key := repoKey{org: source.org, repo: source.repo}
		grouped[key] = append(grouped[key], source)
	}

	flattenedOrgs := sets.New[string](defaultFlattenOrgs...)
	flattenedOrgs.Insert(o.flattenOrgs...)
	if o.org != "" {
		flattenedOrgs.Insert(o.org)
	}

	type repoWork struct {
		key      repoKey
		branches []location
	}
	work := make(chan repoWork)
	var errsMu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < o.parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				repoSyncer := syncer
				repoSyncer.logger = logrus.WithFields(logrus.Fields{"org": item.key.org, "repo": item.key.repo})
				dstRepo := item.key.repo
				if !flattenedOrgs.Has(item.key.org) {
					dstRepo = fmt.Sprintf("%s-%s", item.key.org, item.key.repo)
				}
				repoErrs := repoSyncer.syncRepo(item.key.org, item.key.repo, o.targetOrg, dstRepo, item.branches)
				if len(repoErrs) > 0 {
					errsMu.Lock()
					errs = append(errs, repoErrs...)
					errsMu.Unlock()
				}
			}
		}()
	}

	for key, branches := range grouped {
		work <- repoWork{key: key, branches: branches}
	}
	close(work)
	wg.Wait()

	if len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatal("There were failures")
	}
}

func getWhitelistedLocations(whitelist map[string][]string, git gitFunc, prefix, token string) (map[location]struct{}, []error) {
	var errs []error
	locations := make(map[location]struct{})

	for org, repos := range whitelist {
		for _, repo := range repos {
			remoteURL, err := url.Parse(fmt.Sprintf("%s/%s/%s", prefix, org, repo))
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
