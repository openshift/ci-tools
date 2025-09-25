package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/prow/pkg/logrusutil"
)

const pullRequestInfoComment = "**Pipeline controller notification**\nThis repository is configured to use the [pipeline controller](https://docs.ci.openshift.org/docs/how-tos/creating-a-pipeline/). Second-stage tests will be triggered only if the required tests of the first stage are successful. The pipeline controller will automatically detect which contexts are required and will utilize `/test` Prow commands to trigger the second stage.\n\nFor optional jobs, comment `/test ?` to see a list of all defined jobs. Review these jobs and use `/test <job>` to manually trigger optional jobs most likely to be impacted by the proposed changes."

const RepoNotConfiguredMessage = "This repository is not currently configured for [pipeline controller](https://docs.ci.openshift.org/docs/how-tos/creating-a-pipeline/) support."

type options struct {
	client                   prowflagutil.KubernetesOptions
	github                   prowflagutil.GitHubOptions
	githubEventServerOptions githubeventserver.Options
	config                   configflagutil.ConfigOptions
	configFile               string
	lgtmConfigFile           string
	dryrun                   bool
	webhookSecretFile        string
}

func (o *options) validate() error {
	for _, opt := range []interface{ Validate(bool) error }{&o.client, &o.config} {
		if err := opt.Validate(o.dryrun); err != nil {
			return err
		}
	}

	return nil
}

func (o *options) parseArgs(fs *flag.FlagSet, args []string) error {
	fs.BoolVar(&o.dryrun, "dry-run", false, "Run in dry-run mode.")
	fs.StringVar(&o.configFile, "config-file", "", "Config file with list of enabled orgs and repos.")
	fs.StringVar(&o.lgtmConfigFile, "lgtm-config-file", "", "Config file with list of enabled orgs and repos with second stage triggered by lgtm label.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	o.config.AddFlags(fs)
	o.github.AddFlags(fs)
	o.client.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(args); err != nil {
		logrus.WithError(err).Fatal("Could not parse args.")
	}

	if o.configFile == "" {
		return fmt.Errorf("--config-file is mandatory")
	}
	if o.lgtmConfigFile == "" {
		return fmt.Errorf("--lgtm-config-file is mandatory")
	}
	if err := o.githubEventServerOptions.DefaultAndValidate(); err != nil {
		return err
	}

	return o.validate()
}

func parseOptions() options {
	var o options

	if err := o.parseArgs(flag.CommandLine, os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("invalid flag options")
	}

	return o
}

type clientWrapper struct {
	ghc                minimalGhClient
	configDataProvider *ConfigDataProvider
	watcher            *watcher
	lgtmWatcher        *watcher
}

func (cw *clientWrapper) handlePullRequestCreation(l *logrus.Entry, event github.PullRequestEvent) {
	if github.PullRequestActionOpened == event.Action {
		org := event.Repo.Owner.Login
		repo := event.Repo.Name
		number := event.PullRequest.Number

		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   number,
		})

		// Check if repo is configured for automatic pipelines
		currentCfg := cw.watcher.getConfig()
		repos, orgExists := currentCfg[org]
		repoConfig, repoExists := repos[repo]
		isInConfig := orgExists && repoExists

		if !isInConfig {
			// Repository not in configuration - do not operate on it
			return
		}

		// Check if the repository is in automatic mode
		isAutomaticPipeline := repoConfig.Trigger == "auto"

		// Repo is in configuration, add appropriate comment
		presubmits := cw.configDataProvider.GetPresubmits(org + "/" + repo)
		if isAutomaticPipeline {
			if len(presubmits.protected) > 0 || len(presubmits.alwaysRequired) > 0 ||
				len(presubmits.conditionallyRequired) > 0 || len(presubmits.pipelineConditionallyRequired) > 0 ||
				len(presubmits.pipelineSkipOnlyRequired) > 0 {
				// Repo has pipeline-controlled jobs and is in automatic mode, use pipeline info comment
				if err := cw.ghc.CreateComment(org, repo, number, pullRequestInfoComment); err != nil {
					logger.WithError(err).Error("failed to create comment")
				}
			}
		} else {
			// Manual mode: Check for non-always-run jobs
			cfg := cw.configDataProvider.configGetter()
			presubmits := cfg.GetPresubmitsStatic(org + "/" + repo)

			hasNonAlwaysRunJobs := false
			for _, p := range presubmits {
				if !p.AlwaysRun {
					hasNonAlwaysRunJobs = true
					break
				}
			}

			if hasNonAlwaysRunJobs {
				comment := "There are test jobs defined for this repository which are not configured to run automatically. " +
					"Comment `/test ?` to see a list of all defined jobs. Review these jobs and use `/test <job>` to manually trigger jobs most likely to be impacted by the proposed changes." +
					"Comment `/pipeline required` to trigger all required & necessary jobs."

				if err := cw.ghc.CreateComment(org, repo, number, comment); err != nil {
					logger.WithError(err).Error("failed to create comment")
				}
			}
		}
	}
}

func (cw *clientWrapper) handleLabelAddition(l *logrus.Entry, event github.PullRequestEvent) {
	if github.PullRequestActionLabeled == event.Action && event.Label.Name == labels.LGTM {
		org := event.Repo.Owner.Login
		repo := event.Repo.Name
		currentCfg := cw.lgtmWatcher.getConfig()
		repos, orgExists := currentCfg[org]
		logger := l.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
			"pr":   event.PullRequest.Number,
		})
		_, repoExists := repos[repo]
		if !orgExists || !repoExists {
			return
		}
		prowJob := &v1.ProwJob{
			Spec: v1.ProwJobSpec{
				Refs: &v1.Refs{
					Org:     org,
					Repo:    repo,
					BaseRef: event.PullRequest.Base.Ref,
					Pulls: []v1.Pull{
						{Number: event.PullRequest.Number},
					},
				},
			},
		}
		presubmits := cw.configDataProvider.GetPresubmits(prowJob.Spec.Refs.Org + "/" + prowJob.Spec.Refs.Repo)
		logger.WithField("protected", presubmits.protected).
			WithField("always_required", presubmits.alwaysRequired).
			WithField("conditionally_required", presubmits.conditionallyRequired).
			WithField("pipeline_conditionally_required", presubmits.pipelineConditionallyRequired).
			Debug("found presubmits")
		if len(presubmits.protected) == 0 && len(presubmits.alwaysRequired) == 0 &&
			len(presubmits.conditionallyRequired) == 0 && len(presubmits.pipelineConditionallyRequired) == 0 &&
			len(presubmits.pipelineSkipOnlyRequired) == 0 {
			return
		}

		if err := sendComment(presubmits, prowJob, cw.ghc, func() {}); err != nil {
			logger.WithError(err).Error("failed to send a comment")
		}
	}
}

func (cw *clientWrapper) handleIssueComment(l *logrus.Entry, event github.IssueCommentEvent) {
	// Only handle issue comments on PRs
	if !event.Issue.IsPullRequest() {
		return
	}

	// Check if the comment contains "/pipeline required" with flexible whitespace
	pipelineRequiredRegex := regexp.MustCompile(`(?i)/pipeline\s+required`)
	if !pipelineRequiredRegex.MatchString(event.Comment.Body) {
		return
	}

	org := event.Repo.Owner.Login
	repo := event.Repo.Name
	number := event.Issue.Number

	logger := l.WithFields(logrus.Fields{
		"org":  org,
		"repo": repo,
		"pr":   number,
	})

	// Get presubmits for this repo
	presubmits := cw.configDataProvider.GetPresubmits(org + "/" + repo)

	// Check if there are any pipeline-controlled jobs
	if len(presubmits.protected) == 0 && len(presubmits.alwaysRequired) == 0 &&
		len(presubmits.conditionallyRequired) == 0 && len(presubmits.pipelineConditionallyRequired) == 0 &&
		len(presubmits.pipelineSkipOnlyRequired) == 0 {
		logger.Debug("No pipeline-controlled jobs found for repo")
		return
	}

	// Check if repo is in configuration (either manual or auto mode)
	currentCfg := cw.watcher.getConfig()
	repos, orgExists := currentCfg[org]
	_, repoExists := repos[repo]
	if !orgExists || !repoExists {
		logger.Debug("Repository not in pipeline controller configuration")
		if err := cw.ghc.CreateComment(org, repo, number, RepoNotConfiguredMessage); err != nil {
			logger.WithError(err).Error("failed to create comment")
		}
		return
	}

	// Fetch PR details
	pr, err := cw.ghc.GetPullRequest(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("failed to get PR details")
		return
	}

	// Create a fake ProwJob to reuse existing logic
	prowJob := &v1.ProwJob{
		Spec: v1.ProwJobSpec{
			Refs: &v1.Refs{
				Org:     org,
				Repo:    repo,
				BaseRef: pr.Base.Ref,
				Pulls: []v1.Pull{
					{Number: number, SHA: pr.Head.SHA},
				},
			},
		},
	}

	// Generate the comment with test/override commands
	if err := sendCommentWithMode(presubmits, prowJob, cw.ghc, func() {}, true); err != nil {
		logger.WithError(err).Error("failed to send comment in response to /pipeline required")
	}
}

// handlePipelineContextCreation handles PR events (open, push, reopen) and creates contexts for matching tests
func (cw *clientWrapper) handlePipelineContextCreation(l *logrus.Entry, event github.PullRequestEvent) {
	if event.Action != github.PullRequestActionOpened &&
		event.Action != github.PullRequestActionSynchronize &&
		event.Action != github.PullRequestActionReopened {
		return
	}

	org := event.Repo.Owner.Login
	repo := event.Repo.Name
	number := event.PullRequest.Number
	sha := event.PullRequest.Head.SHA

	presubmits := cw.configDataProvider.GetPresubmits(org + "/" + repo)
	if len(presubmits.pipelineConditionallyRequired) == 0 && len(presubmits.pipelineSkipOnlyRequired) == 0 &&
		len(presubmits.protected) == 0 {
		return
	}

	currentCfg := cw.watcher.getConfig()
	repos, orgExists := currentCfg[org]
	_, repoExists := repos[repo]
	if !orgExists || !repoExists {
		return
	}

	logger := l.WithFields(logrus.Fields{
		"org":  org,
		"repo": repo,
		"pr":   number,
		"sha":  sha,
	})

	// Get changed files for this PR
	changedFiles, err := cw.ghc.GetPullRequestChanges(org, repo, number)
	if err != nil {
		logger.WithError(err).Error("failed to get PR changes")
		return
	}

	filenames := make([]string, 0, len(changedFiles))
	for _, change := range changedFiles {
		filenames = append(filenames, change.Filename)
	}

	// Evaluate pipeline_run_if_changed tests
	for _, presubmit := range presubmits.pipelineConditionallyRequired {
		if pattern, ok := presubmit.Annotations["pipeline_run_if_changed"]; ok && pattern != "" {
			if shouldRun, err := matchesPattern(pattern, filenames); err != nil {
				logger.WithError(err).WithField("test", presubmit.Name).WithField("pattern", pattern).Error("failed to evaluate pattern")
				continue
			} else if shouldRun {
				if err := cw.createContext(org, repo, sha, presubmit.Name, "pending", "Pipeline controller will trigger this test"); err != nil {
					logger.WithError(err).WithField("test", presubmit.Name).Error("failed to create context")
				} else {
					logger.WithField("test", presubmit.Name).Info("created pending context for pipeline test")
				}
			}
		}
	}

	// Evaluate pipeline_skip_only_if_changed tests
	for _, presubmit := range presubmits.pipelineSkipOnlyRequired {
		if pattern, ok := presubmit.Annotations["pipeline_skip_only_if_changed"]; ok && pattern != "" {
			if shouldSkip, err := allFilesMatchPattern(pattern, filenames); err != nil {
				logger.WithError(err).WithField("test", presubmit.Name).WithField("pattern", pattern).Error("failed to evaluate skip pattern")
				continue
			} else if !shouldSkip {
				// If not all files match the skip pattern, we should run the test
				if err := cw.createContext(org, repo, sha, presubmit.Name, "pending", "Pipeline controller will trigger this test"); err != nil {
					logger.WithError(err).WithField("test", presubmit.Name).Error("failed to create context")
				} else {
					logger.WithField("test", presubmit.Name).Info("created pending context for pipeline test")
				}
			}
		}
	}

	// Create contexts for protected jobs (always_run: false, optional: false, no run conditions)
	for _, presubmitName := range presubmits.protected {
		if err := cw.createContext(org, repo, sha, presubmitName, "pending", "Pipeline controller will trigger this test"); err != nil {
			logger.WithError(err).WithField("test", presubmitName).Error("failed to create context")
		} else {
			logger.WithField("test", presubmitName).Info("created pending context for protected test")
		}
	}
}

// createContext creates a GitHub status context
func (cw *clientWrapper) createContext(org, repo, sha, context, state, description string) error {
	return cw.ghc.CreateStatus(org, repo, sha, github.Status{
		Context:     context,
		State:       state,
		Description: description,
	})
}

// matchesPattern checks if any of the filenames match the given regex pattern
func matchesPattern(pattern string, filenames []string) (bool, error) {
	if pattern == "" {
		return false, nil
	}

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	for _, filename := range filenames {
		if regex.MatchString(filename) {
			return true, nil
		}
	}

	return false, nil
}

// allFilesMatchPattern checks if ALL filenames match the given regex pattern
func allFilesMatchPattern(pattern string, filenames []string) (bool, error) {
	if pattern == "" {
		return false, nil
	}

	if len(filenames) == 0 {
		return false, nil
	}

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	for _, filename := range filenames {
		if !regex.MatchString(filename) {
			return false, nil
		}
	}

	return true, nil
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("component", "pipeline-controller")
	ctrlruntimelog.SetLogger(logrusr.New(logger))

	o := parseOptions()

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logger.WithError(err).Fatal("error starting config agent")
	}
	cfg := configAgent.Config

	restCfg, err := o.client.InfrastructureClusterConfig(o.dryrun)
	if err != nil {
		logger.WithError(err).Fatal("failed to get kubeconfig")
	}
	mgr, err := manager.New(restCfg, manager.Options{
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				cfg().ProwJobNamespace: {},
			},
		},
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		logger.WithError(err).Fatal("failed to create manager")
	}

	if err := o.client.AddKubeconfigChangeCallback(func() {
		logger.Info("kubeconfig changed, exiting to trigger a restart")
		interrupts.Terminate()
	}); err != nil {
		logger.WithError(err).Fatal("failed to register kubeconfig callback")
	}

	if o.github.TokenPath != "" {
		if err := secret.Add(o.github.TokenPath); err != nil {
			logger.WithError(err).Fatal("error reading GitHub credentials")
		}
	}

	githubClient, err := o.github.GitHubClient(o.dryrun)
	if err != nil {
		logger.WithError(err).Fatal("error getting GitHub client")
	}

	watcher := newWatcher(o.configFile, logger)
	go watcher.watch()

	lgtmWatcher := newWatcher(o.lgtmConfigFile, logger)
	go lgtmWatcher.watch()

	// Create a function that returns repos from both config and lgtm config
	repoLister := func() []string {
		var repos []string

		// Get repos from main config
		mainConfig := watcher.getConfig()
		for org, repoConfigs := range mainConfig {
			for repo := range repoConfigs {
				repos = append(repos, org+"/"+repo)
			}
		}

		// Get repos from lgtm config
		lgtmConfig := lgtmWatcher.getConfig()
		for org, repoConfigs := range lgtmConfig {
			for repo := range repoConfigs {
				orgRepo := org + "/" + repo
				// Avoid duplicates
				found := false
				for _, existing := range repos {
					if existing == orgRepo {
						found = true
						break
					}
				}
				if !found {
					repos = append(repos, orgRepo)
				}
			}
		}

		return repos
	}

	configDataProvider := NewConfigDataProvider(cfg, repoLister, logger.WithField("component", "config-data-provider"))
	go configDataProvider.Run()

	reconciler, err := NewReconciler(mgr, configDataProvider, githubClient, logger, watcher)
	if err != nil {
		logger.WithError(err).Fatal("failed to construct github reporter controller")
	}
	go reconciler.cleanOldIds(24 * time.Hour)

	if err = secret.Add(o.github.TokenPath, o.webhookSecretFile); err != nil {
		logger.WithError(err).Fatal("error starting secrets agent")
	}
	webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)
	cw := &clientWrapper{
		ghc:                githubClient,
		configDataProvider: configDataProvider,
		watcher:            watcher,
		lgtmWatcher:        lgtmWatcher,
	}

	logger.Debug("starting event server")
	eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
	eventServer.RegisterHandlePullRequestEvent(cw.handlePullRequestCreation)
	eventServer.RegisterHandlePullRequestEvent(cw.handleLabelAddition)
	eventServer.RegisterHandlePullRequestEvent(cw.handlePipelineContextCreation)
	eventServer.RegisterHandleIssueCommentEvent(cw.handleIssueComment)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.Run(func(ctx context.Context) {
		if err := mgr.Start(ctx); err != nil {
			logger.WithError(err).Fatal("controller manager exited with error")
		}
	})
	interrupts.WaitForGracefulShutdown()
}
