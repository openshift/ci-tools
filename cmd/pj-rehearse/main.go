package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/rehearse"
)

type options struct {
	logLevel string

	serverMode        bool
	dryRun            bool
	debugLogPath      string
	prowjobKubeconfig string
	kubernetesOptions flagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	preCheck            bool
	commentOnPrCreation bool //TODO: this is useful for a soft rollout of the plugin. remove once that is complete

	normalLimit int
	moreLimit   int
	maxLimit    int

	releaseRepoPath string //TODO: this will be removed when the old pj-rehearse job is gone
	rehearsalLimit  int    //TODO: this will be removed when the old pj-rehearse job is gone

	webhookSecretFile        string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	git                      prowflagutil.GitOptions
}

func gatherOptions() (options, error) {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))

	fs.BoolVar(&o.serverMode, "server", false, "Run as a github event server, external prow plugin rather than as a job")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")

	fs.BoolVar(&o.preCheck, "pre-check", false, "If true, check for rehearsable jobs and provide the list upon PR creation")
	fs.BoolVar(&o.preCheck, "comment-on-pr-creation", true, "If true, provide an explanatory comment when a new PR is opened in a repo with the plugin configured.")

	fs.IntVar(&o.normalLimit, "normal-limit", 10, "Upper limit of jobs attempted to rehearse with normal command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.moreLimit, "more-limit", 20, "Upper limit of jobs attempted to rehearse with more command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.maxLimit, "max-limit", 35, "Upper limit of jobs attempted to rehearse with max command (if more jobs are being touched, only this many will be rehearsed)")

	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	fs.IntVar(&o.rehearsalLimit, "rehearsal-limit", 35, "Upper limit of jobs attempted to rehearse (if more jobs are being touched, only this many will be rehearsed)")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	var errs []error
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		errs = append(errs, fmt.Errorf("invalid log level specified: %w", err))
	}
	logrus.SetLevel(level)

	errs = append(errs, o.kubernetesOptions.Validate(o.dryRun))

	if o.serverMode {
		errs = append(errs, o.githubEventServerOptions.DefaultAndValidate())
		errs = append(errs, o.github.Validate(o.dryRun))
	} else {
		if len(o.releaseRepoPath) == 0 {
			errs = append(errs, errors.New("--candidate-path was not provided"))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func rehearsalConfigFromOptions(o options) rehearse.RehearsalConfig {
	return rehearse.RehearsalConfig{
		ProwjobKubeconfig: o.prowjobKubeconfig,
		KubernetesOptions: o.kubernetesOptions,
		NoTemplates:       o.noTemplates,
		NoRegistry:        o.noRegistry,
		NoClusterProfiles: o.noClusterProfiles,
		DryRun:            o.dryRun,
		NormalLimit:       o.normalLimit,
		MoreLimit:         o.moreLimit,
		MaxLimit:          o.maxLimit,
	}
}

const (
	misconfigurationOutput = `ERROR: pj-rehearse: misconfiguration

pj-rehearse could not process its necessary inputs properly. No rehearsal
jobs were run. This is likely a pj-rehearse job configuration problem.`
	rehearseFailureOutput = `ERROR: pj-rehearse: rehearsal tool failure

pj-rehearse attempted to submit jobs for rehearsal, but it failed to either
submit them or to fetch their results. This is either a pj-rehearse bug or
an infrastructure issue.`
	jobsFailureOutput = `ERROR: pj-rehearse: rehearsed jobs failure

pj-rehearse rehearsed jobs and at least one of them failed. This means that
job would fail when executed against the current HEAD of the target branch.`
	failedSetupOutput = `ERROR: pj-rehearse: setup failure

pj-rehearse failed to finish all setup necessary to perform job rehearsals.
This is either a pj-rehearse bug or an infrastructure failure.`
	jobValidationOutput = `ERROR: pj-rehearse: failed to validate rehearsal jobs

pj-rehearse created invalid rehearsal jobs.This is either a pj-rehearse bug, or
the rehearsed jobs themselves are invalid.`
)

func rehearseAsJob(o options) error {
	jobSpec, err := pjdwapi.ResolveSpecFromEnv()
	if err != nil {
		logrus.WithError(err).Error("could not read JOB_SPEC")
		return fmt.Errorf(misconfigurationOutput)
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)

	if jobSpec.Type != pjapi.PresubmitJob {
		logger.Info("Not able to rehearse jobs when not run in the context of a presubmit job")
		// Exiting successfully will make pj-rehearsal job not fail when run as a
		// in a batch job. Such failures would be confusing and unactionable
		return nil
	}

	pr := jobSpec.Refs.Pulls[0]
	org, repo, prNumber := jobSpec.Refs.Org, jobSpec.Refs.Repo, pr.Number
	logger.Infof("Rehearsing Prow jobs for configuration PR %s/%s#%d", org, repo, prNumber)

	rc := rehearsalConfigFromOptions(o)
	candidate := rehearse.RehearsalCandidateFromJobSpec(jobSpec)
	presubmits, periodics, changedTemplates, changedClusterProfiles, err := rc.DetermineAffectedJobs(candidate, o.releaseRepoPath, logger)
	if err != nil {
		return fmt.Errorf("error determining affected jobs: %w: %s", err, misconfigurationOutput)
	}
	if len(presubmits) == 0 && len(periodics) == 0 {
		// Nothing to rehearse
		return nil
	}

	debugLogger := logrus.New()
	debugLogger.Level = logrus.DebugLevel
	if o.debugLogPath != "" {
		if f, err := os.OpenFile(o.debugLogPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, os.ModePerm); err == nil {
			defer f.Close()
			debugLogger.Out = f
		} else {
			logger.WithError(err).Error("could not open debug log file")
			return fmt.Errorf(failedSetupOutput)
		}
	}
	loggers := rehearse.Loggers{Job: logger, Debug: debugLogger.WithField(prowgithub.PrLogField, prNumber)}

	prConfig, prRefs, imageStreamTags, presubmitsToRehearse, err := rc.SetupJobs(candidate, o.releaseRepoPath, presubmits, periodics, changedTemplates, changedClusterProfiles, o.rehearsalLimit, loggers)
	if err != nil {
		return fmt.Errorf("error setting up jobs: %w: %s", err, failedSetupOutput)
	}

	if len(presubmitsToRehearse) > 0 {
		if err := prConfig.Prow.ValidateJobConfig(); err != nil {
			return fmt.Errorf("%s: %w", jobValidationOutput, err)
		}

		jobsTriggered, err := rc.RehearseJobs(candidate, o.releaseRepoPath, prConfig, prRefs, imageStreamTags, presubmitsToRehearse, changedTemplates, changedClusterProfiles, loggers)
		if err != nil {
			if jobsTriggered {
				return fmt.Errorf(jobsFailureOutput)
			} else {
				return fmt.Errorf(rehearseFailureOutput)
			}
		}
	}

	return nil
}

func rehearsalServer(o options) {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", "pj-rehearse")

	if err := secret.Add(o.github.TokenPath, o.webhookSecretFile); err != nil {
		logger.WithError(err).Fatal("Error starting secrets agent.")
	}
	webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)

	s, err := serverFromOptions(o)
	if err != nil {
		logger.WithError(err).Fatal("couldn't create server")
	}

	logger.Debug("starting eventServer")
	eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
	if o.commentOnPrCreation {
		eventServer.RegisterHandlePullRequestEvent(s.handlePullRequestCreation)
	}
	eventServer.RegisterHandleIssueCommentEvent(s.handleIssueComment)
	eventServer.RegisterHelpProvider(s.helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	if err := imagev1.Install(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("failed to register imagev1 scheme")
	}

	if o.serverMode {
		rehearsalServer(o)
	} else {
		if err := rehearseAsJob(o); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}
}
