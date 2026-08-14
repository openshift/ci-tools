package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const pluginName = "in-repo-config"

type options struct {
	logLevel                 string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	webhookSecretFile        string
	jobConfigDir             string
	prowgenImage             string
	checkconfigImage         string
	prowConfigPath           string
	namespace                string
	dispatcherURL            string
	maxConcurrentHandlers    int
	maxQueuedHandlers        int
	queueTimeoutMinutes      int
	handlerTimeoutMinutes    int
	gcIntervalMinutes        int
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.jobConfigDir, "job-config-dir", "", "Path to the EFS-mounted job config directory.")
	fs.StringVar(&o.prowgenImage, "prowgen-image", "", "Container image for ci-operator-prowgen used in bootstrap jobs.")
	fs.StringVar(&o.checkconfigImage, "checkconfig-image", "", "Container image for ci-operator-checkconfig used in the config-check presubmit.")
	fs.StringVar(&o.prowConfigPath, "prow-config-path", "/etc/config/config.yaml", "Path to the Prow config file for ProwJob defaulting.")
	fs.StringVar(&o.dispatcherURL, "dispatcher-url", "", "URL of the prow-job-dispatcher for cluster assignment. When set, all triggered jobs get their cluster from this service.")
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace where ProwJobs will be created.")
	fs.IntVar(&o.maxConcurrentHandlers, "max-concurrent-handlers", 3, "Maximum number of webhook handlers running concurrently.")
	fs.IntVar(&o.maxQueuedHandlers, "max-queued-handlers", 20, "Maximum number of webhook handlers waiting in queue.")
	fs.IntVar(&o.queueTimeoutMinutes, "queue-timeout-minutes", 5, "How long a queued handler waits before being dropped.")
	fs.IntVar(&o.handlerTimeoutMinutes, "handler-timeout-minutes", 10, "How long a handler can run before the caller stops waiting.")
	fs.IntVar(&o.gcIntervalMinutes, "gc-interval-minutes", 30, "Interval for garbage collecting stale ephemeral job directories.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	if _, err := logrus.ParseLevel(o.logLevel); err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	if o.jobConfigDir == "" {
		return fmt.Errorf("--job-config-dir must be set")
	}
	if o.prowgenImage == "" {
		return fmt.Errorf("--prowgen-image must be set")
	}
	if o.checkconfigImage == "" {
		return fmt.Errorf("--checkconfig-image must be set")
	}
	return o.githubEventServerOptions.DefaultAndValidate()
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", pluginName)

	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logger.Fatalf("Invalid options: %v", err)
	}

	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	var tokens []string
	if o.github.TokenPath != "" {
		tokens = append(tokens, o.github.TokenPath)
	}
	if o.github.AppPrivateKeyPath != "" {
		tokens = append(tokens, o.github.AppPrivateKeyPath)
	}
	tokens = append(tokens, o.webhookSecretFile)

	if err := secret.Add(tokens...); err != nil {
		logger.WithError(err).Fatal("Error starting secrets agent.")
	}

	getWebhookHMAC := secret.GetTokenGenerator(o.webhookSecretFile)

	githubClient, err := o.github.GitHubClient(false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.WithError(err).Fatal("Error getting in-cluster config.")
	}
	scheme := k8sruntime.NewScheme()
	if err := pjapi.AddToScheme(scheme); err != nil {
		logger.WithError(err).Fatal("Error adding ProwJob scheme.")
	}
	pjclient, err := ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{Scheme: scheme})
	if err != nil {
		logger.WithError(err).Fatal("Error creating ProwJob client.")
	}

	var dispatcherClient dispatcher.Client
	if o.dispatcherURL != "" {
		dispatcherClient = dispatcher.NewClient(o.dispatcherURL)
		logger.WithField("url", o.dispatcherURL).Info("Using dispatcher for cluster assignment")
	}

	serv := &server{
		ghc:              githubClient,
		pjclient:         pjclient,
		prowConfigPath:   o.prowConfigPath,
		namespace:        o.namespace,
		jobConfigDir:     o.jobConfigDir,
		prowgenImage:     o.prowgenImage,
		checkconfigImage: o.checkconfigImage,
		dispatcher:       dispatcherClient,
	}

	queueTimeout := time.Duration(o.queueTimeoutMinutes) * time.Minute
	executionTimeout := time.Duration(o.handlerTimeoutMinutes) * time.Minute
	throttler := newWebhookThrottler(o.maxConcurrentHandlers, o.maxQueuedHandlers, queueTimeout, executionTimeout)

	eventServer := githubeventserver.New(o.githubEventServerOptions, getWebhookHMAC, logger)
	eventServer.RegisterHandlePullRequestEvent(func(l *logrus.Entry, event github.PullRequestEvent) {
		throttler.handle(l, func() { serv.handlePullRequest(l, event) })
	})
	eventServer.RegisterPushEventHandler(func(l *logrus.Entry, event github.PushEvent) {
		throttler.handle(l, func() { serv.handlePush(l, event) })
	})
	eventServer.RegisterHelpProvider(helpProvider, logger)

	ctx, cancel := context.WithCancel(context.Background())
	gcInterval := time.Duration(o.gcIntervalMinutes) * time.Minute
	go serv.startEphemeralGC(ctx, gcInterval, logger)

	interrupts.OnInterrupt(func() {
		cancel()
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
