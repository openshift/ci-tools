package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// Config maps upstreams to downstreams for verification
type Config struct {
	// Repositories is a mapping of downstream org/repo to upstream org/repo
	Repositories map[string]string `json:"repositories,omitempty"`
}

func (c *Config) validate() error {
	var errs []error
	for downstreamRepo, upstreamRepo := range c.Repositories {
		if len(strings.Split(downstreamRepo, "/")) != 2 {
			return fmt.Errorf("%s should be in org/repo format", downstreamRepo)
		}

		if len(strings.Split(upstreamRepo, "/")) != 2 {
			return fmt.Errorf("%s should be in org/repo format", upstreamRepo)
		}
	}

	return utilerrors.NewAggregate(errs)
}

type options struct {
	mut *sync.RWMutex

	configPath        string
	webhookSecretFile string

	config *Config

	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions

	dryRun bool
}

func gatherOptions() options {
	o := options{
		mut: &sync.RWMutex{},
	}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.configPath, "config-path", "", "Path to backport verifier configuration.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "", "Path to the file containing the GitHub HMAC secret.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	if err := o.github.Validate(o.dryRun); err != nil {
		return err
	}

	bytes, err := gzip.ReadFileMaybeGZIP(o.configPath)
	if err != nil {
		return fmt.Errorf("couldn't read configuration file: %v", o.configPath)
	}

	var config Config
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		return fmt.Errorf("couldn't unmarshal configuration: %w", err)
	}
	o.config = &config

	if err := o.config.validate(); err != nil {
		return err
	}

	if err := o.githubEventServerOptions.DefaultAndValidate(); err != nil {
		return err
	}

	return nil
}

func (o *options) getConfigWatchAndUpdate() (func(ctx context.Context), error) {
	errFunc := func(err error, msg string) {
		logrus.WithError(err).Error(msg)
	}

	eventFunc := func() error {
		bytes, err := gzip.ReadFileMaybeGZIP(o.configPath)
		if err != nil {
			return fmt.Errorf("couldn't read configuration file %s: %w", o.configPath, err)
		}

		var c Config
		if err := yaml.Unmarshal(bytes, &c); err != nil {
			return fmt.Errorf("couldn't unmarshal configuration: %w", err)
		}

		if err := c.validate(); err != nil {
			return err
		}

		o.mut.Lock()
		defer o.mut.Unlock()
		o.config = &c
		logrus.Info("Configuration updated")

		return nil
	}
	watcher, err := prowconfig.GetCMMountWatcher(eventFunc, errFunc, filepath.Dir(o.configPath))
	if err != nil {
		return nil, fmt.Errorf("couldn't get the file watcher: %w", err)
	}

	return watcher, nil
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", "backport-verifier")

	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logger.Fatalf("Invalid options: %v", err)
	}

	configWatchAndUpdate, err := o.getConfigWatchAndUpdate()
	if err != nil {
		logger.WithError(err).Fatal("couldn't get config file watch and update function")
	}
	interrupts.Run(configWatchAndUpdate)

	if err := secret.Add(o.github.TokenPath, o.webhookSecretFile); err != nil {
		logger.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		config: func() *Config {
			o.mut.Lock()
			defer o.mut.Unlock()
			return o.config
		},
		ghc: githubClient,
	}

	eventServer := githubeventserver.New(o.githubEventServerOptions, secret.GetTokenGenerator(o.webhookSecretFile), logger)
	eventServer.RegisterHandleIssueCommentEvent(serv.handleIssueComment)
	eventServer.RegisterHandlePullRequestEvent(serv.handlePullRequestEvent)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
