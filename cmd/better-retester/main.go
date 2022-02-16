package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/pkg/flagutil"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"
)

type githubClient interface {
	GetCombinedStatus(org, repo, ref string) (*github.CombinedStatus, error)
	QueryWithGitHubAppsSupport(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error
}

type options struct {
	config configflagutil.ConfigOptions
	github prowflagutil.GitHubOptions

	runOnce bool
	dryRun  bool

	interval time.Duration
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	var intervalRaw string

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.BoolVar(&o.runOnce, "run-once", false, "If true, run only once then quit.")
	fs.StringVar(&intervalRaw, "interval", "1h", "Parseable duration string that specifies the sync period")

	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		group.AddFlags(fs)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	var err error
	o.interval, err = time.ParseDuration(intervalRaw)
	if err != nil {
		logrus.WithError(err).Fatal("could not parse interval")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	gc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client")
	}

	gitClient, err := o.github.GitClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Git client.")
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	c := newController(gc, configAgent.Config, git.ClientFactoryFrom(gitClient), o.github.AppPrivateKeyPath != "")

	interrupts.OnInterrupt(func() {
		if err := gitClient.Clean(); err != nil {
			logrus.WithError(err).Error("Could not clean up git client cache.")
		}
	})

	execute(c)
	if o.runOnce {
		return
	}

	// This a sleep that can be interrupted :)
	select {
	case <-interrupts.Context().Done():
		return
	case <-time.After(o.interval):
	}

	interrupts.Tick(func() { execute(c) }, func() time.Duration { return o.interval })
	interrupts.WaitForGracefulShutdown()
}

func execute(c *retestController) {
	if err := c.sync(); err != nil {
		logrus.WithError(err).Error("Error syncing")
	}
}
