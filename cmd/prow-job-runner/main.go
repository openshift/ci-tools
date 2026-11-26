package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/prow-job-runner"
)

// run parses the command line and passes the selected options to the runner.
func run(ctx context.Context, fs *flag.FlagSet, args []string, out io.Writer) error {
	var opts prowjobrunner.Options
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Print configured presubmits to stdout")
	fs.StringVar(&opts.ReleaseRepo, "release-repo", "/home/prow/go/src/github.com/openshift/release", "Path to a checkout of openshift/release")
	fs.StringVar(&opts.TestPlan, "test-plan", "", "Local path or gs:// URL of the test plan YAML (required)")
	fs.StringVar(&opts.PullRequest, "pull-request", "", "GitHub pull request, as <org>/<repo>#<number>")
	fs.StringVar(&opts.PullRequestData, "pull-request-data", "", "Path to a GitHub pull request API response (JSON)")
	fs.StringVar(&opts.ProwJobNamespace, "pj-namespace", "", "Namespace for ProwJob resources (defaults to prowjob_namespace from the release checkout)")
	opts.Kubernetes.AddFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional argument %q", fs.Arg(0))
	}
	return prowjobrunner.Run(ctx, opts, out)
}

// main runs prow-job-runner and reports errors on stderr.
func main() {
	logrusutil.ComponentInit()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fs := flag.NewFlagSet("prow-job-runner", flag.ExitOnError)
	if err := run(ctx, fs, os.Args[1:], os.Stdout); err != nil {
		logrus.WithError(err).Fatal("prow-job-runner failed")
	}
}
