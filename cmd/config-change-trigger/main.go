package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pjutil"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	dryRun bool
	limit  int

	releaseRepoPath string
	flagutil.GitHubOptions
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit jobs to Prow")
	fs.IntVar(&o.limit, "limit", 30, "Maximum number of jobs to trigger.")

	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	o.AddFlags(fs)
	o.AllowAnonymous = true

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	if len(o.releaseRepoPath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := secret.Add(o.GitHubOptions.TokenPath); err != nil {
		logrus.WithError(err).Fatal("error starting secrets agent.")
	}

	githubClient, err := o.GitHubOptions.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	var jobSpec *pjdwapi.JobSpec
	if jobSpec, err = pjdwapi.ResolveSpecFromEnv(); err != nil {
		logrus.WithError(err).Fatal("could not read JOB_SPEC")
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

	prConfig, err := config.GetAllConfigs(o.releaseRepoPath)
	if err != nil {
		logger.WithError(err).Warn("could not load all configuration from candidate revision of release repo")
	}
	masterConfig, err := config.GetAllConfigsFromSHA(o.releaseRepoPath, fmt.Sprintf("%s^1", jobSpec.Refs.BaseSHA))
	if err != nil {
		logger.WithError(err).Fatal("could not load configuration from base revision of release repo")
	}

	// We always need both  versions of ciop config
	if prConfig.CiOperator == nil || masterConfig.CiOperator == nil {
		logger.WithError(err).Fatal("could not load ci-operator configs from base or tested revision of release repo")
	}
	changedCiopConfigs, _ := diffs.GetChangedCiopConfigs(masterConfig.CiOperator, prConfig.CiOperator, logger)
	changedImagesPostsubmits := diffs.GetImagesPostsubmitsForCiopConfigs(prConfig.Prow, changedCiopConfigs)

	namespace := prConfig.Prow.ProwJobNamespace

	var pjclient ctrlruntimeclient.Client
	if o.dryRun {
		pjclient = fakectrlruntimeclient.NewClientBuilder().Build()
	} else {
		pjclient, err = ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{})
	}
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob client")
	}

	jobs, errs := jobsFor(changedImagesPostsubmits, githubClient, prConfig.Prow)
	if len(jobs) > o.limit {
		logger.Infof("Truncating %d changed jobs to a limit of %d.", len(jobs), o.limit)
		jobs = jobs[:o.limit]
	}
	for _, job := range jobs {
		job.Namespace = namespace
		logger = logger.WithFields(pjutil.ProwJobFields(&job))
		if err := pjclient.Create(context.Background(), &job); err != nil {
			errs = append(errs, err)
			logger.WithError(err).Warn("failed to start ProwJob")
			continue
		}
		logger.Info("Started ProwJob")
	}
	if len(errs) > 0 {
		logger.WithError(utilerrors.NewAggregate(errs)).Fatal("failed to start all changed images postsubmits")
	}
}

type refGetter interface {
	GetRef(org, repo, ref string) (string, error)
}

func jobsFor(changedImagesPostsubmits []diffs.PostsubmitInContext, getter refGetter, prowConfig *prowconfig.Config) ([]v1.ProwJob, []error) {
	var jobs []v1.ProwJob
	var errs []error
	for _, data := range changedImagesPostsubmits {
		sha, err := getter.GetRef(data.Metadata.Org, data.Metadata.Repo, fmt.Sprintf("heads/%s", data.Metadata.Branch))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		refs := v1.Refs{
			Org:     data.Metadata.Org,
			Repo:    data.Metadata.Repo,
			BaseRef: data.Metadata.Branch,
			BaseSHA: sha,
		}
		jobs = append(jobs, pjutil.NewProwJob(pjutil.PostsubmitSpec(data.Job, refs), data.Job.Labels, data.Job.Annotations, pjutil.RequireScheduling(prowConfig.Scheduler.Enabled)))
	}
	return jobs, errs
}
