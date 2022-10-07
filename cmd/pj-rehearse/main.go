package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes/scheme"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/flagutil"
	prowgithub "k8s.io/test-infra/prow/github"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/rehearse"
)

type options struct {
	dryRun            bool
	debugLogPath      string
	prowjobKubeconfig string
	kubernetesOptions flagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	releaseRepoPath string
	rehearsalLimit  int
}

func gatherOptions() (options, error) {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")

	fs.IntVar(&o.rehearsalLimit, "rehearsal-limit", 35, "Upper limit of jobs attempted to rehearse (if more jobs are being touched, only this many will be rehearsed)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	if len(o.releaseRepoPath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return o.kubernetesOptions.Validate(o.dryRun)
}

func rehearsalConfigFromOptions(o options) rehearse.RehearsalConfig {
	return rehearse.RehearsalConfig{
		ProwjobKubeconfig: o.prowjobKubeconfig,
		KubernetesOptions: o.kubernetesOptions,
		NoTemplates:       o.noTemplates,
		NoRegistry:        o.noRegistry,
		NoClusterProfiles: o.noClusterProfiles,
		DryRun:            o.dryRun,
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

func rehearseMain() error {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	if err := imagev1.Install(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("failed to register imagev1 scheme")
	}

	var jobSpec *pjdwapi.JobSpec
	if jobSpec, err = pjdwapi.ResolveSpecFromEnv(); err != nil {
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

	return nil
}

func main() {
	if err := rehearseMain(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
