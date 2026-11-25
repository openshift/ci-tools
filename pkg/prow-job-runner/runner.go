// Package prowjobrunner configures Prow presubmits for ci-operator tests on a pull request.
package prowjobrunner

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/prow-job-runner/presubmits"
)

// Options selects the release checkout, ci-operator tests, pull request, and execution mode.
type Options struct {
	ReleaseRepo     string
	TestPlan        string // Required local path or gs:// URL of the test plan.
	PullRequest     string // GitHub org/repo#number; mutually exclusive with PullRequestData.
	PullRequestData string // Path to a saved GitHub pull request API response.
	DryRun          bool
}

// Run resolves all requested tests before writing their ProwJob manifests to out in dry-run mode.
// If neither PR source is supplied, it uses JOB_SPEC. Live submission returns an error.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	switch {
	case opts.ReleaseRepo == "":
		return errors.New("--release-repo must be a non-empty path")
	case opts.PullRequest != "" && opts.PullRequestData != "":
		return errors.New("--pull-request and --pull-request-data are mutually exclusive")
	case opts.TestPlan == "":
		return errors.New("--test-plan is required")
	}
	tests, err := readTestPlan(ctx, opts.TestPlan, readGCSObject)
	if err != nil {
		return fmt.Errorf("reading test plan %q: %w", opts.TestPlan, err)
	}
	refs, err := readRefs(ctx, opts.PullRequest, opts.PullRequestData)
	if err != nil {
		return fmt.Errorf("reading PR info: %w", err)
	}
	pull := refs.Pulls[0]
	logrus.WithFields(logrus.Fields{
		"org": refs.Org, "repo": refs.Repo, "pr": pull.Number,
		"base_ref": refs.BaseRef, "base_sha": refs.BaseSHA, "head_sha": pull.SHA,
	}).Info("Resolved pull request")
	jobs, err := presubmits.ForTests(opts.ReleaseRepo, tests, refs)
	if err != nil {
		return err
	}
	if !opts.DryRun {
		return errors.New("running presubmits: Not Implemented")
	}
	for _, job := range jobs {
		data, err := yaml.Marshal(job)
		if err != nil {
			return fmt.Errorf("encoding %s: %w", job.Spec.Job, err)
		}
		if _, err := fmt.Fprintf(out, "---\n%s", data); err != nil {
			return fmt.Errorf("writing %s: %w", job.Spec.Job, err)
		}
	}
	logrus.WithField("job_count", len(jobs)).Info("Wrote dry-run ProwJob manifests")
	return nil
}
