package main

import (
	"context"
	"errors"
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

type Config struct {
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
	gitName           string
	gitEmail          string
	githubLogin       string
	webhookSecretFile string

	config *Config

	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions

	dryRun bool
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")

	fs.StringVar(&o.configPath, "config-path", "/etc/config/config.yaml", "Path to publicize configuration.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	fs.StringVar(&o.githubLogin, "github-login", "", "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	if o.githubLogin == "" {
		return errors.New("--github-login must be specified")
	}

	if (o.gitEmail == "") != (o.gitName == "") {
		return errors.New("--git-name and --git-email must be specified")
	}

	if err := o.github.Validate(o.dryRun); err != nil {
		return err
	}

	bytes, err := gzip.ReadFileMaybeGZIP(o.configPath)
	if err != nil {
		return fmt.Errorf("couldn't read publicize configuration file: %v", o.configPath)
	}

	if err := yaml.Unmarshal(bytes, &o.config); err != nil {
		return fmt.Errorf("couldn't unmarshal publicize configuration: %w", err)
	}

	if err := o.config.validate(); err != nil {
		return err
	}

	if err := o.githubEventServerOptions.DefaultAndValidate(); err != nil {
		return err
	}

	m := sync.RWMutex{}
	o.mut = &m

	return nil
}

func (o *options) getConfigWatchAndUpdate() (func(ctx context.Context), error) {
	errFunc := func(err error, msg string) {
		logrus.WithError(err).Error(msg)
	}

	eventFunc := func() error {
		bytes, err := gzip.ReadFileMaybeGZIP(o.configPath)
		if err != nil {
			return fmt.Errorf("couldn't read publicize configuration file %s: %w", o.configPath, err)
		}

		var c *Config
		if err := yaml.Unmarshal(bytes, &c); err != nil {
			return fmt.Errorf("couldn't unmarshal publicize configuration: %w", err)
		}

		if err := c.validate(); err != nil {
			return err
		}

		o.mut.Lock()
		defer o.mut.Unlock()
		o.config = c
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
	logger := logrus.WithField("plugin", "publicize")

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

	webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)
	githubTokenGenerator := secret.GetTokenGenerator(o.github.TokenPath)

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	gitClient, err := o.github.GitClientFactory("", nil, o.dryRun, false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting Git client.")
	}

	serv := &server{
		githubTokenGenerator: githubTokenGenerator,
		config: func() *Config {
			o.mut.Lock()
			defer o.mut.Unlock()
			return o.config
		},
		ghc:         githubClient,
		gc:          gitClient,
		gitName:     o.gitName,
		gitEmail:    o.gitEmail,
		githubLogin: o.githubLogin,
		githubHost:  o.github.Host,
		dry:         o.dryRun,
	}

	eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
	eventServer.RegisterHandleIssueCommentEvent(serv.handleIssueComment)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
		if err := gitClient.Clean(); err != nil {
			logrus.WithError(err).Error("Could not clean up git client cache.")
		}
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
