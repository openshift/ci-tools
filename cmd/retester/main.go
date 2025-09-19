package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/sirupsen/logrus"

	prowConfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/metrics"

	"github.com/openshift/ci-tools/pkg/retester"
)

type options struct {
	config configflagutil.ConfigOptions
	github prowflagutil.GitHubOptions

	runOnce bool
	dryRun  bool

	intervalRaw       string
	cacheRecordAgeRaw string

	interval time.Duration

	cacheFile      string
	cacheFileOnS3  bool
	cacheRecordAge time.Duration

	configFile string
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}
	if o.configFile == "" {
		return fmt.Errorf("--config-file is required")
	}
	if o.cacheFileOnS3 && o.cacheFile == "" {
		return fmt.Errorf("--cache-file is required if --cache-file-on-s3 is set to true")
	}
	return nil
}

func (o *options) complete() error {
	var err error
	o.interval, err = time.ParseDuration(o.intervalRaw)
	if err != nil {
		return fmt.Errorf("invalid --interval: %w", err)
	}
	o.cacheRecordAge, err = time.ParseDuration(o.cacheRecordAgeRaw)
	if err != nil {
		return fmt.Errorf("invalid --cache-record-age: %w", err)
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.BoolVar(&o.runOnce, "run-once", false, "If true, run only once then quit.")
	fs.BoolVar(&o.cacheFileOnS3, "cache-file-on-s3", false, "If true, use aws s3 bucket to store the cache file.")
	fs.StringVar(&o.intervalRaw, "interval", "1h", "Parseable duration string that specifies the sync period")
	fs.StringVar(&o.cacheFile, "cache-file", "", "File to persist cache. No persistence of cache if not set")
	fs.StringVar(&o.cacheRecordAgeRaw, "cache-record-age", "168h", "Parseable duration string that specifies how long a cache record lives in cache after the last time it was considered")
	fs.StringVar(&o.configFile, "config-file", "", "Path to the configure file of the retest.")

	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		group.AddFlags(fs)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	return o
}

func main() {
	o := gatherOptions()
	if err := o.complete(); err != nil {
		logrus.WithError(err).Fatal("failed to complete options")
	}
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("failed to validate options")
	}

	gc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client")
	}

	gitClient, err := o.github.GitClientFactory("", nil, o.dryRun, false)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Git client.")
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	config, err := retester.LoadConfig(o.configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load config from file")
	}

	ctx := interrupts.Context()

	var awsConfig aws.Config
	if o.cacheFileOnS3 {
		awsConfig, err = awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
		if err != nil {
			logrus.WithError(err).Fatal("Failed to create AWS config.")
		}
		_, err = awsConfig.Credentials.Retrieve(ctx)
		if err != nil {
			logrus.WithError(err).Fatal("Error getting AWS credentials.")
		}
	}

	c := retester.NewController(ctx, gc, configAgent.Config, gitClient, o.github.AppPrivateKeyPath != "", o.cacheFile, o.cacheRecordAge, config, &awsConfig)

	metrics.ExposeMetrics("retester", prowConfig.PushGateway{}, prowflagutil.DefaultMetricsPort)

	interrupts.OnInterrupt(func() {
		if err := gitClient.Clean(); err != nil {
			logrus.WithError(err).Error("Could not clean up git client cache.")
		}
	})

	execute(ctx, c)
	if o.runOnce {
		return
	}

	// This a sleep that can be interrupted :)
	select {
	case <-interrupts.Context().Done():
		return
	case <-time.After(o.interval):
	}

	interrupts.Tick(func() { execute(ctx, c) }, func() time.Duration { return o.interval })
	interrupts.WaitForGracefulShutdown()
}

func execute(ctx context.Context, c *retester.RetestController) {
	if err := c.Run(ctx); err != nil {
		logrus.WithError(err).Error("Error running")
	}
}
