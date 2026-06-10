package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	prowConfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	pprofutil "sigs.k8s.io/prow/pkg/pjutil/pprof"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/rehearse"
)

var concurrentHandlersInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "pj_rehearse_handlers_in_flight",
	Help: "Current number of concurrent webhook handler goroutines running in pj-rehearse.",
})

var queuedHandlers = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "pj_rehearse_handlers_queued",
	Help: "Current number of webhook handler requests queued in pj-rehearse.",
})

var droppedHandlerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "pj_rehearse_handler_requests_dropped_total",
	Help: "Total number of webhook handler requests dropped by pj-rehearse admission control.",
}, []string{"reason"})

var timedOutHandlers = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "pj_rehearse_handler_timeouts_total",
	Help: "Total number of webhook handler requests that exceeded execution timeout in pj-rehearse.",
})

func init() {
	prometheus.MustRegister(concurrentHandlersInFlight)
	prometheus.MustRegister(queuedHandlers)
	prometheus.MustRegister(droppedHandlerRequests)
	prometheus.MustRegister(timedOutHandlers)
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

func (d *handlerDispatcher) dispatch(logger *logrus.Entry, handler func(), onDrop func(string)) {
	if d.tryAcquireExecutionSlot() {
		d.run(logger, handler)
		return
	}

	if !d.tryQueue() {
		droppedHandlerRequests.WithLabelValues("queue_full").Inc()
		logger.WithField("max_queue", cap(d.queueSlots)).Warn("Dropping webhook request because handler queue is full")
		if onDrop != nil {
			onDrop("queue_full")
		}
		return
	}
	select {
	case d.executionSlots <- struct{}{}:
		d.dequeue()
		d.run(logger, handler)
	case <-time.After(d.queueTimeout):
		d.dequeue()
		droppedHandlerRequests.WithLabelValues("queue_timeout").Inc()
		logger.WithField("timeout", d.queueTimeout).Warn("Dropping webhook request because it waited too long in queue")
		if onDrop != nil {
			onDrop("queue_timeout")
		}
	}
}

// run executes the handler in a goroutine and waits for completion or timeout.
// On timeout, the goroutine keeps running (and holding its execution slot) until
// the handler returns naturally. True cancellation would require context propagation
// through all downstream operations (git, GitHub API, config loading).
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
				logger.WithField("panic", r).Errorf("Webhook handler panicked: %v\n%s", r, stack)
			}
		}()
		handler()
	}()

	select {
	case <-done:
	case <-time.After(d.executionTimeout):
		timedOutHandlers.Inc()
		logger.WithField("timeout", d.executionTimeout).Warn("Webhook handler exceeded execution timeout and is still running in background")
	}
}

func (d *handlerDispatcher) tryAcquireExecutionSlot() bool {
	select {
	case d.executionSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (d *handlerDispatcher) tryQueue() bool {
	select {
	case d.queueSlots <- struct{}{}:
		queuedHandlers.Inc()
		return true
	default:
		return false
	}
}

func (d *handlerDispatcher) dequeue() {
	select {
	case <-d.queueSlots:
		queuedHandlers.Dec()
	default:
		logrus.Warn("dequeue called but queue slot channel was empty; gauge may drift")
	}
}

type options struct {
	logLevel               string
	instrumentationOptions prowflagutil.InstrumentationOptions

	prowjobKubeconfig string
	kubernetesOptions prowflagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool

	normalLimit           int
	moreLimit             int
	maxLimit              int
	maxConcurrentHandlers int
	maxQueuedHandlers     int
	queueTimeoutMinutes   int
	handlerTimeoutMinutes int

	gcsBucket          string
	gcsCredentialsFile string
	gcsBrowserPrefix   string

	dryRun        bool
	dryRunOptions dryRunOptions

	stickyLabelAuthors prowflagutil.Strings

	webhookSecretFile        string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	config                   configflagutil.ConfigOptions
}

func gatherOptions() (options, error) {
	o := options{kubernetesOptions: prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	o.instrumentationOptions.AddFlags(fs)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Run in integration test mode; no event server is created, and no jobs are submitted")
	o.dryRunOptions.bind(fs)

	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")

	fs.IntVar(&o.normalLimit, "normal-limit", 10, "Upper limit of jobs attempted to rehearse with normal command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.moreLimit, "more-limit", 20, "Upper limit of jobs attempted to rehearse with more command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.maxLimit, "max-limit", 35, "Upper limit of jobs attempted to rehearse with max command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.maxConcurrentHandlers, "max-concurrent-handlers", 5, "Maximum number of webhook handlers that may run concurrently.")
	fs.IntVar(&o.maxQueuedHandlers, "max-queued-handlers", 50, "Maximum number of webhook handler requests queued while all handler slots are busy.")
	fs.IntVar(&o.queueTimeoutMinutes, "queue-timeout-minutes", 5, "Maximum time in minutes a request can wait in the queue before being dropped.")
	fs.IntVar(&o.handlerTimeoutMinutes, "handler-timeout-minutes", 15, "Maximum time in minutes a handler can execute before being considered timed out.")

	fs.Var(&o.stickyLabelAuthors, "sticky-label-author", "PR Author for which the 'rehearsals-ack' label will not be removed upon a new push. Can be passed multiple times.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	fs.StringVar(&o.gcsBucket, "gcs-bucket", "test-platform-results", "GCS Bucket to upload affected jobs list")
	fs.StringVar(&o.gcsCredentialsFile, "gcs-credentials-file", "/etc/gcs/service-account.json", "GCS Credentials file to upload affected jobs list")
	fs.StringVar(&o.gcsBrowserPrefix, "gcs-browser-prefix", "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/", "Prefix for the GCS Browser for viewing the affected jobs list")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)
	o.config.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	var errs []error
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		errs = append(errs, fmt.Errorf("invalid log level specified: %w", err))
	}
	logrus.SetLevel(level)
	if o.maxConcurrentHandlers < 1 {
		errs = append(errs, errors.New("max-concurrent-handlers must be greater than zero"))
	}
	if o.maxQueuedHandlers < 1 {
		errs = append(errs, errors.New("max-queued-handlers must be greater than zero"))
	}
	if o.queueTimeoutMinutes < 1 {
		errs = append(errs, errors.New("queue-timeout-minutes must be greater than zero"))
	}
	if o.handlerTimeoutMinutes < 1 {
		errs = append(errs, errors.New("handler-timeout-minutes must be greater than zero"))
	}

	if o.dryRun {
		errs = append(errs, o.dryRunOptions.validate())
	} else {
		errs = append(errs, o.githubEventServerOptions.DefaultAndValidate())
		errs = append(errs, o.github.Validate(o.dryRun))
		errs = append(errs, o.config.Validate(o.dryRun))
		errs = append(errs, o.kubernetesOptions.Validate(o.dryRun))
	}

	return utilerrors.NewAggregate(errs)
}

func notifyDroppedRequest(ghc githubClient, org, repo string, number int, user, reason string, queueTimeoutMinutes int, userTriggered bool, logger *logrus.Entry) {
	reasonText := "the request queue is currently full"
	if reason == "queue_timeout" {
		reasonText = fmt.Sprintf("the request waited in queue for longer than %d minutes", queueTimeoutMinutes)
	}
	var comment string
	if userTriggered {
		comment = fmt.Sprintf("@%s: your `/pj-rehearse` request was not processed because %s. Please retry in a few minutes.", user, reasonText)
	} else {
		comment = fmt.Sprintf("@%s: `pj-rehearse` could not automatically process this event because %s. Use `/pj-rehearse` to trigger rehearsals manually.", user, reasonText)
	}
	if err := ghc.CreateComment(org, repo, number, comment); err != nil {
		logger.WithError(err).Warn("failed to create dropped-request notification comment")
	}
}

type dryRunOptions struct {
	dryRunPath     string
	pullRequestVar string
	limit          int
	testNamespace  string
}

func (o *dryRunOptions) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.dryRunPath, "dry-run-path", "", "Path to a openshift/release working copy with a revision to be tested")
	fs.StringVar(&o.pullRequestVar, "pull-request-var", "PR", "Name of ENV var containing the PullRequest JSON")
	fs.IntVar(&o.limit, "limit", 20, "Upper limit of jobs attempted to rehearse")
	fs.StringVar(&o.testNamespace, "test-namespace", "test-namespace", "The namespace to use for prowjobs AND pods")
}

func (o *dryRunOptions) validate() error {
	if o.dryRunPath == "" {
		return errors.New("dry-run-path must be supplied when in dry-run mode")
	}
	return nil
}

func rehearsalConfigFromOptions(o options) rehearse.RehearsalConfig {
	return rehearse.RehearsalConfig{
		ProwjobKubeconfig:  o.prowjobKubeconfig,
		KubernetesOptions:  o.kubernetesOptions,
		NoRegistry:         o.noRegistry,
		DryRun:             o.dryRun,
		NormalLimit:        o.normalLimit,
		MoreLimit:          o.moreLimit,
		MaxLimit:           o.maxLimit,
		StickyLabelAuthors: o.stickyLabelAuthors.StringSet(),
		GCSBucket:          o.gcsBucket,
		GCSCredentialsFile: o.gcsCredentialsFile,
		GCSBrowserPrefix:   o.gcsBrowserPrefix,
	}
}

func dryRun(o options, logger *logrus.Entry) error {
	dro := o.dryRunOptions
	rc := rehearsalConfigFromOptions(o)
	rc.ProwjobNamespace = dro.testNamespace
	rc.PodNamespace = dro.testNamespace

	prEnv, ok := os.LookupEnv(dro.pullRequestVar)
	if !ok {
		logrus.Fatal("couldn't get PR from env")
	}
	pr := &github.PullRequest{}
	if err := json.Unmarshal([]byte(prEnv), pr); err != nil {
		logrus.WithError(err).Fatal("couldn't unmarshall PR")
	}

	candidatePath := dro.dryRunPath
	candidate := rehearse.RehearsalCandidateFromPullRequest(pr, pr.Base.SHA)

	prConfig, presubmits, periodics, _, err := rc.DetermineAffectedJobs(candidate, candidatePath, false, logger)
	if err != nil {
		return fmt.Errorf("error determining affected jobs: %w: %s", err, "ERROR: pj-rehearse: misconfiguration")
	}

	prConfig, prRefs, presubmitsToRehearse, err := rc.SetupJobs(candidate, candidatePath, prConfig, presubmits, periodics, dro.limit, logger)
	if err != nil {
		return fmt.Errorf("error setting up jobs: %w: %s", err, "ERROR: pj-rehearse: setup failure")
	}

	if len(presubmitsToRehearse) > 0 {
		if err := prConfig.Prow.ValidateJobConfig(); err != nil {
			return fmt.Errorf("%s: %w", "ERROR: pj-rehearse: failed to validate rehearsal jobs", err)
		}

		_, err := rc.RehearseJobs(candidatePath, prRefs, presubmitsToRehearse, prConfig.Prow, true, logger)
		return err
	}

	return nil
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", "pj-rehearse")

	o, err := gatherOptions()
	if err != nil {
		logger.WithError(err).Fatal("failed to gather options")
	}
	if err := o.validate(); err != nil {
		logger.WithError(err).Fatal("invalid options")
	}
	if err := imagev1.Install(scheme.Scheme); err != nil {
		logger.WithError(err).Fatal("failed to register imagev1 scheme")
	}

	if o.dryRun {
		if err = dryRun(o, logger); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	} else {
		pprofutil.Instrument(o.instrumentationOptions)
		metrics.ExposeMetrics("pj-rehearse", prowConfig.PushGateway{}, o.instrumentationOptions.MetricsPort)

		if err = secret.Add(o.webhookSecretFile); err != nil {
			logger.WithError(err).Fatal("Error starting secrets agent.")
		}
		webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)

		s, err := serverFromOptions(o)
		if err != nil {
			logger.WithError(err).Fatal("couldn't create server")
		}

		logger.Debug("starting eventServer")
		eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
		queueTimeout := time.Duration(o.queueTimeoutMinutes) * time.Minute
		executionTimeout := time.Duration(o.handlerTimeoutMinutes) * time.Minute
		dispatcher := newHandlerDispatcher(o.maxConcurrentHandlers, o.maxQueuedHandlers, queueTimeout, executionTimeout)
		eventServer.RegisterHandlePullRequestEvent(func(l *logrus.Entry, event github.PullRequestEvent) {
			if event.Action != github.PullRequestActionOpened && event.Action != github.PullRequestActionSynchronize {
				return
			}
			dispatcher.dispatch(l, func() {
				s.handlePullRequestCreation(l, event)
				s.handleNewPush(l, event)
			}, func(reason string) {
				notifyDroppedRequest(s.ghc, event.Repo.Owner.Login, event.Repo.Name, event.Number, event.PullRequest.User.Login, reason, o.queueTimeoutMinutes, false, l)
			})
		})
		eventServer.RegisterHandleIssueCommentEvent(func(l *logrus.Entry, event github.IssueCommentEvent) {
			if !event.Issue.IsPullRequest() || event.Action != github.IssueCommentActionCreated || !commentRegex.MatchString(event.Comment.Body) {
				return
			}
			dispatcher.dispatch(l, func() {
				s.handleIssueComment(l, event)
			}, func(reason string) {
				notifyDroppedRequest(s.ghc, event.Repo.Owner.Login, event.Repo.Name, event.Issue.Number, event.Comment.User.Login, reason, o.queueTimeoutMinutes, true, l)
			})
		})
		eventServer.RegisterHelpProvider(s.helpProvider, logger)

		interrupts.OnInterrupt(func() {
			eventServer.GracefulShutdown()
		})

		health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
		health.ServeReady()

		interrupts.ListenAndServe(eventServer, time.Second*30)
		interrupts.WaitForGracefulShutdown()
	}
}
