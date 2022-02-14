package main

import (
	"flag"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

type githubClient interface {
	// TODO
}

type options struct {
	github prowflagutil.GitHubOptions

	dryRun bool
}

func (o *options) Validate() error {
	if err := o.github.Validate(o.dryRun); err != nil {
		return err
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")

	o.github.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	_, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client.")
	}

	// TODO: Should this be a Tide-like loop or should we react on events (or both)?

	// Input: Tide Config
	// Output: A list of PRs that would merge but have failing jobs
	candidates := findCandidates()

	// Input: A list of PRs that would merge but have failing jobs
	// Output: A subset of input PRs whose jobs are actually required for merge
	candidates = atLeastOneFailingRequiredJob(candidates)

	// Input: A list of PRs that would merge but have failing required jobs
	// Output: A subset of input PRs that are *not* in a back-off (whatever the back-off is)
	candidates = notInBackOff(candidates)

	// TODO: One day I will be useful
	time.Sleep(time.Hour)
}

func findCandidates() []interface{} {
	return []interface{}{}
}

func atLeastOneFailingRequiredJob(input []interface{}) []interface{} {
	return input
}

func notInBackOff(input []interface{}) []interface{} {
	return input
}
