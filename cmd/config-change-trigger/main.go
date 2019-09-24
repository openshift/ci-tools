package main

import (
	"flag"
	"fmt"
	"github.com/openshift/ci-tools/pkg/util"
	"os"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/errorutil"
	"k8s.io/test-infra/prow/flagutil"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pjutil"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

type options struct {
	dryRun bool
	local  bool

	releaseRepoPath string
	flagutil.GitHubOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit jobs to Prow")
	fs.BoolVar(&o.local, "local", false, "Whether this is a local execution or part of a CI job")

	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	o.AddFlagsWithoutDefaultGitHubTokenPath(fs)

	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	if len(o.releaseRepoPath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("error starting secrets agent.")
	}

	githubClient, err := o.GitHubOptions.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	var jobSpec *pjdwapi.JobSpec
	if o.local {
		if jobSpec, err = config.NewLocalJobSpec(o.releaseRepoPath); err != nil {
			logrus.WithError(err).Fatal("could not create local JobSpec")
		}
	} else {
		if jobSpec, err = pjdwapi.ResolveSpecFromEnv(); err != nil {
			logrus.WithError(err).Fatal("could not read JOB_SPEC")
		}
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)
	logger.Info("Triggering post-submit Prow jobs for a configuration change")

	var clusterConfig *rest.Config
	if !o.dryRun {
		clusterConfig, err = util.LoadClusterConfig()
		if err != nil {
			logger.WithError(err).Fatal("could not load cluster clusterConfig")
		}
	}

	prConfig := config.GetAllConfigs(o.releaseRepoPath, logger)
	masterConfig, err := config.GetAllConfigsFromSHA(o.releaseRepoPath, fmt.Sprintf("%s^1", jobSpec.Refs.BaseSHA), logger)
	if err != nil {
		logger.WithError(err).Fatal("could not load configuration from base revision of release repo")
	}

	// We always need both  versions of ciop config
	if prConfig.CiOperator == nil || masterConfig.CiOperator == nil {
		logger.WithError(err).Fatal("could not load ci-operator configs from base or tested revision of release repo")
	}
	changedCiopConfigs, _ := diffs.GetChangedCiopConfigs(masterConfig.CiOperator, prConfig.CiOperator, logger)
	changedImagesPostsubmits := diffs.GetImagesPostsubmitsForCiopConfigs(masterConfig.Prow, changedCiopConfigs)

	namespace := prConfig.Prow.ProwJobNamespace
	if o.local {
		namespace = config.StagingNamespace
	}

	pjclient, err := rehearse.NewProwJobClient(clusterConfig, namespace, o.dryRun)
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob client")
	}

	jobs, errs := jobsFor(changedImagesPostsubmits, githubClient)
	for _, job := range jobs {
		logger = logger.WithFields(pjutil.ProwJobFields(&job))
		if _, err := pjclient.Create(&job); err != nil {
			errs = append(errs, err)
			logger.WithError(err).Warn("failed to start ProwJob")
			continue
		}
		logger.Info("Started ProwJob")
	}
	if len(errs) > 0 {
		logger.WithError(errorutil.NewAggregate(errs...)).Fatal("failed to start all changed images postsubmits")
	}
}

type refGetter interface {
	GetRef(org, repo, ref string) (string, error)
}

func jobsFor(changedImagesPostsubmits []diffs.PostsubmitInContext, getter refGetter) ([]v1.ProwJob, []error) {
	var jobs []v1.ProwJob
	var errs []error
	for _, data := range changedImagesPostsubmits {
		sha, err := getter.GetRef(data.Info.Org, data.Info.Repo, fmt.Sprintf("heads/%s", data.Info.Branch))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		refs := v1.Refs{
			Org:     data.Info.Org,
			Repo:    data.Info.Repo,
			BaseRef: data.Info.Branch,
			BaseSHA: sha,
		}
		jobs = append(jobs, pjutil.NewProwJob(pjutil.PostsubmitSpec(data.Job, refs), data.Job.Labels, data.Job.Annotations))
	}
	return jobs, errs
}
