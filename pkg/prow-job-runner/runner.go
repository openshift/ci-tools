// Package prowjobrunner coordinates test-plan loading, PR resolution, presubmit configuration, and execution.
package prowjobrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/validation"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/prow-job-runner/execution"
	"github.com/openshift/ci-tools/pkg/prow-job-runner/presubmits"
	"github.com/openshift/ci-tools/pkg/prow-job-runner/pullrequest"
	"github.com/openshift/ci-tools/pkg/prow-job-runner/testplan"
)

// pollInterval is the delay between status checks of submitted ProwJobs.
const pollInterval = 10 * time.Second

// Options selects the release checkout, ci-operator tests, pull request, and execution mode.
type Options struct {
	ReleaseRepo      string
	TestPlan         string // Required local path or gs:// URL of the test plan.
	PullRequest      string // GitHub org/repo#number; mutually exclusive with PullRequestData.
	PullRequestData  string // Path to a saved GitHub pull request API response.
	ProwJobNamespace string // Optional override for the release configuration's prowjob_namespace.
	DryRun           bool
	Kubernetes       flagutil.KubernetesOptions
}

// Run resolves all requested tests, then submits and waits for them or writes their manifests to out in dry-run mode.
// If neither PR source is supplied, it uses JOB_SPEC. It returns an error if any submitted job fails.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	if err := opts.validate(); err != nil {
		return err
	}
	jobs, err := configureJobs(ctx, opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return writeManifests(out, jobs)
	}
	client, err := execution.NewClient(&opts.Kubernetes)
	if err != nil {
		return err
	}
	return execution.SubmitAndWait(ctx, client, jobs, pollInterval)
}

// validate checks required inputs, mutually exclusive PR sources, and Kubernetes options before resolving jobs.
func (opts Options) validate() error {
	switch {
	case opts.ReleaseRepo == "":
		return errors.New("--release-repo must be a non-empty path")
	case opts.PullRequest != "" && opts.PullRequestData != "":
		return errors.New("--pull-request and --pull-request-data are mutually exclusive")
	case opts.TestPlan == "":
		return errors.New("--test-plan is required")
	}
	if opts.ProwJobNamespace != "" {
		if errs := validation.IsDNS1123Label(opts.ProwJobNamespace); len(errs) != 0 {
			return fmt.Errorf("invalid --pj-namespace %q: %s", opts.ProwJobNamespace, strings.Join(errs, "; "))
		}
	}
	if err := opts.Kubernetes.Validate(opts.DryRun); err != nil {
		return fmt.Errorf("validating Kubernetes options: %w", err)
	}
	return nil
}

// configureJobs resolves the requested tests and PR into jobs with the selected namespace.
func configureJobs(ctx context.Context, opts Options) ([]*prowapi.ProwJob, error) {
	planReader := testplan.Reader{}
	tests, err := planReader.Read(ctx, opts.TestPlan)
	if err != nil {
		return nil, fmt.Errorf("reading test plan %q: %w", opts.TestPlan, err)
	}
	prReader := pullrequest.Reader{}
	refs, err := prReader.Read(ctx, opts.PullRequest, opts.PullRequestData)
	if err != nil {
		return nil, fmt.Errorf("reading PR info: %w", err)
	}
	jobs, err := presubmits.ForTests(opts.ReleaseRepo, tests, refs)
	if err != nil {
		return nil, err
	}
	if opts.ProwJobNamespace != "" {
		for _, job := range jobs {
			job.Namespace = opts.ProwJobNamespace
		}
	}
	return jobs, nil
}

// writeManifests writes one YAML document per job to out in the requested order.
func writeManifests(out io.Writer, jobs []*prowapi.ProwJob) error {
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
