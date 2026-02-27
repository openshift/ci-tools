package main

import (
	"errors"
	"flag"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

type options struct {
	prcreation.PRCreationOptions
	prTitle          string
	prMessage        string
	gitCommitMessage string
	prAssignee       string
	organization     string
	repo             string
	branch           string
	head             string
}

func gatherOptions() (*options, error) {
	opts := options{}
	opts.PRCreationOptions.AddFlags(flag.CommandLine)
	flag.StringVar(&opts.prTitle, "pr-title", "", "The title of the PR to create")
	flag.StringVar(&opts.prMessage, "pr-message", "", "The message of the PR to create")
	flag.StringVar(&opts.gitCommitMessage, "git-message", "", "The git commit message of the PR to create. If not set, then its value will be composed of other flags")
	flag.StringVar(&opts.prAssignee, "pr-assignee", "", "Comma separated list of assignees for the PR to create")
	flag.StringVar(&opts.organization, "organization", "openshift", "The GitHub organization in which the PR should be created")
	flag.StringVar(&opts.repo, "repo", "release", "The name of the repo in which the PR should be created")
	flag.StringVar(&opts.branch, "branch", "main", "The branch for which the PR should be created")
	flag.StringVar(&opts.head, "head", "", "Pre-pushed head ref (e.g. user:branch). When set, skips fork/commit/push and only creates/updates the PR")
	flag.Parse()

	var errs []error
	if opts.prTitle == "" {
		errs = append(errs, errors.New("--pr-title is mandatory"))
	}
	if opts.organization == "" {
		errs = append(errs, errors.New("--organization is mandatory"))
	}
	if opts.repo == "" {
		errs = append(errs, errors.New("--repo is mandatory"))
	}
	if opts.branch == "" {
		errs = append(errs, errors.New("--branch is mandatory"))
	}

	if err := opts.PRCreationOptions.Finalize(); err != nil {
		errs = append(errs, err)
	}

	return &opts, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	var prOpts []prcreation.PrOption
	prOpts = append(prOpts, prcreation.PrBody(opts.prMessage))
	prOpts = append(prOpts, prcreation.PrAssignee(opts.prAssignee))
	prOpts = append(prOpts, prcreation.GitCommitMessage(opts.gitCommitMessage))
	if opts.head != "" {
		prOpts = append(prOpts, prcreation.WithHead(opts.head))
	}

	if err := opts.PRCreationOptions.UpsertPR(".",
		opts.organization,
		opts.repo,
		opts.branch,
		opts.prTitle,
		prOpts...,
	); err != nil {
		logrus.WithError(err).Fatal("failed to upsert PR")
	}
}
