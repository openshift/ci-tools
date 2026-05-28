package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const pluginName = "in-repo-config"

var (
	concurrentHandlersInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "in_repo_config_concurrent_handlers",
		Help: "Number of webhook handlers currently executing",
	})
	droppedHandlerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "in_repo_config_dropped_requests_total",
		Help: "Number of webhook requests dropped",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(concurrentHandlersInFlight)
	prometheus.MustRegister(droppedHandlerRequests)
}

type handlerDispatcher struct {
	executionSlots   chan struct{}
	queueSlots       chan struct{}
	queueTimeout     time.Duration
	executionTimeout time.Duration
}

func newHandlerDispatcher(maxConcurrent, maxQueued int, queueTimeout, executionTimeout time.Duration) *handlerDispatcher {
	return &handlerDispatcher{
		executionSlots:   make(chan struct{}, maxConcurrent),
		queueSlots:       make(chan struct{}, maxQueued),
		queueTimeout:     queueTimeout,
		executionTimeout: executionTimeout,
	}
}

func (d *handlerDispatcher) dispatch(logger *logrus.Entry, handler func()) {
	select {
	case d.executionSlots <- struct{}{}:
		d.run(logger, handler)
		return
	default:
	}

	select {
	case d.queueSlots <- struct{}{}:
	default:
		droppedHandlerRequests.WithLabelValues("queue_full").Inc()
		logger.Warn("dropping webhook request: handler queue is full")
		return
	}

	select {
	case d.executionSlots <- struct{}{}:
		<-d.queueSlots
		d.run(logger, handler)
	case <-time.After(d.queueTimeout):
		<-d.queueSlots
		droppedHandlerRequests.WithLabelValues("queue_timeout").Inc()
		logger.Warn("dropping webhook request: waited too long in queue")
	}
}

func (d *handlerDispatcher) run(logger *logrus.Entry, handler func()) {
	done := make(chan struct{})
	go func() {
		concurrentHandlersInFlight.Inc()
		defer concurrentHandlersInFlight.Dec()
		defer func() { <-d.executionSlots }()
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				stack := make([]byte, 8192)
				stack = stack[:runtime.Stack(stack, false)]
				logger.Errorf("webhook handler panicked: %v\n%s", r, stack)
			}
		}()
		handler()
	}()

	select {
	case <-done:
	case <-time.After(d.executionTimeout):
		logger.Warn("webhook handler exceeded execution timeout, still running in background")
	}
}

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

	serv := &server{
		ghc:              githubClient,
		pjclient:         pjclient,
		prowConfigPath:   o.prowConfigPath,
		namespace:        o.namespace,
		jobConfigDir:     o.jobConfigDir,
		prowgenImage:     o.prowgenImage,
		checkconfigImage: o.checkconfigImage,
	}

	queueTimeout := time.Duration(o.queueTimeoutMinutes) * time.Minute
	executionTimeout := time.Duration(o.handlerTimeoutMinutes) * time.Minute
	dispatcher := newHandlerDispatcher(o.maxConcurrentHandlers, o.maxQueuedHandlers, queueTimeout, executionTimeout)

	eventServer := githubeventserver.New(o.githubEventServerOptions, getWebhookHMAC, logger)
	eventServer.RegisterHandlePullRequestEvent(func(l *logrus.Entry, event github.PullRequestEvent) {
		dispatcher.dispatch(l, func() { serv.handlePullRequest(l, event) })
	})
	eventServer.RegisterPushEventHandler(func(l *logrus.Entry, event github.PushEvent) {
		dispatcher.dispatch(l, func() { serv.handlePush(l, event) })
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
