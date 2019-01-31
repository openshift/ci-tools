package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	prowgithub "k8s.io/test-infra/prow/github"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/rehearse"
)

func loadClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}

	credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("could not load credentials from config: %v", err)
	}

	clusterConfig, err = clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

type options struct {
	dryRun bool
	noFail bool

	configPath    string
	jobConfigPath string

	candidatePath string
}

func getMasterProwConfig(repoPath, sha, configPath, jobConfigPath string) (*prowconfig.Config, error) {
	currentSHA, err := getCurrentSHA(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get SHA of current HEAD: %v", err)
	}

	if err := gitCheckout(repoPath, sha); err != nil {
		return nil, fmt.Errorf("failed to check out baseline revision: %v", err)
	}

	masterProwConfig, err := prowconfig.Load(configPath, jobConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load baseline Prow config: %v", err)
	}

	if err := gitCheckout(repoPath, currentSHA); err != nil {
		return nil, fmt.Errorf("failed to check out tested revision back: %v", err)
	}

	return masterProwConfig, nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")
	fs.BoolVar(&o.noFail, "no-fail", true, "Whether to actually end unsuccessfuly when something breaks")

	fs.StringVar(&o.candidatePath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")

	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	if len(o.candidatePath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return nil
}

func gracefulExit(suppressFailures bool) {
	if suppressFailures {
		os.Exit(0)
	}

	os.Exit(1)
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	jobSpec, err := pjdwapi.ResolveSpecFromEnv()
	if err != nil {
		logrus.WithError(err).Error("could not read JOB_SPEC")
		gracefulExit(o.noFail)
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)

	if jobSpec.Type != v1.PresubmitJob {
		logger.Info("Not able to rehearse jobs when not run in the context of a presubmit job")
		// Exiting successfuly will make pj-rehearsal job not fail when run as a
		// in a batch job. Such failures would be confusing and unactionable
		os.Exit(0)
	}

	prNumber := jobSpec.Refs.Pulls[0].Number
	logger = logrus.WithField(prowgithub.PrLogField, prNumber)

	logger.Info("Rehearsing Prow jobs for a configuration PR")

	configPath := filepath.Join(o.candidatePath, diffs.ConfigInRepoPath)
	jobConfigPath := filepath.Join(o.candidatePath, diffs.JobConfigInRepoPath)

	prowPRConfig, err := prowconfig.Load(configPath, jobConfigPath)
	if err != nil {
		logger.WithError(err).Error("Failed to load PR's Prow config")
		gracefulExit(o.noFail)
	}

	masterProwConfig, err := getMasterProwConfig(o.candidatePath, jobSpec.Refs.BaseSHA, configPath, jobConfigPath)
	if err != nil {
		logger.WithError(err).Error("Failed to load master Prow config")
		gracefulExit(o.noFail)
	}

	prowjobNamespace := masterProwConfig.ProwJobNamespace

	var clusterConfig *rest.Config
	if !o.dryRun {
		clusterConfig, err = loadClusterConfig()
		if err != nil {
			logger.WithError(err).Error("could not load cluster clusterConfig")
			gracefulExit(o.noFail)
		}
	}

	pjclient, err := rehearse.NewProwJobClient(clusterConfig, prowjobNamespace, o.dryRun)
	if err != nil {
		logger.WithError(err).Error("could not create a ProwJob client")
		gracefulExit(o.noFail)
	}

	changedPresubmits, err := diffs.GetChangedPresubmits(masterProwConfig, prowPRConfig)
	if err != nil {
		logger.WithError(err).Error("Failed to determine which jobs should be rehearsed")
		gracefulExit(o.noFail)
	}

	if err := rehearse.ExecuteJobs(changedPresubmits, prNumber, o.candidatePath, jobSpec.Refs, !o.dryRun, logger, pjclient); err != nil {
		logger.WithError(err).Error("Failed to execute rehearsal jobs")
		gracefulExit(o.noFail)
	}
}

func getCurrentSHA(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	sha, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("'%s' failed with error=%v", cmd.Args, err)
	}

	return strings.TrimSpace(string(sha)), nil
}

func gitCheckout(candidatePath, baseSHA string) error {
	cmd := exec.Command("git", "checkout", baseSHA)
	cmd.Dir = candidatePath
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("'%s' failed with out: %s and error %v", cmd.Args, stdoutStderr, err)
	}
	return nil
}
