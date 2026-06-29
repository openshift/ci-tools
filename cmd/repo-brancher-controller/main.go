package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	pprofutil "sigs.k8s.io/prow/pkg/pjutil/pprof"
)

const componentName = "repo-brancher-controller"

type options struct {
	instrumentation                                  flagutil.InstrumentationOptions
	github                                           flagutil.GitHubOptions
	githubEventServer                                githubeventserver.Options
	configDir, forwardingConfigPath, pluginConfigDir string
	hmacPath                                         string
	workers, maxRetries                              int
	configReload, fullResync, shutdownGracePeriod    time.Duration
	retryExhaustedDelay                              time.Duration
	maxConfigStaleness                               time.Duration
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configDir, "config-dir", "", "Path to the ci-operator configuration directory.")
	fs.StringVar(&o.forwardingConfigPath, "forwarding-config", "", "Path to the default and release branch forwarding configuration.")
	fs.StringVar(&o.pluginConfigDir, "plugin-config-dir", "", "Path to Prow's sharded plugin configuration used to verify external-plugin coverage.")
	fs.StringVar(&o.hmacPath, "hmac-secret-file", "/etc/webhook/hmac", "Path to the GitHub webhook HMAC secret.")
	fs.IntVar(&o.workers, "workers", 8, "Number of repositories reconciled concurrently.")
	fs.IntVar(&o.maxRetries, "max-retries", 5, "Maximum fast reconciliation retries per repository.")
	fs.DurationVar(&o.retryExhaustedDelay, "retry-exhausted-delay", 15*time.Minute, "Delay between reconciliation attempts after fast retries are exhausted.")
	fs.DurationVar(&o.configReload, "config-reload-period", 5*time.Minute, "How often to reload desired state from configuration.")
	fs.DurationVar(&o.fullResync, "full-resync-period", 12*time.Hour, "Safety-net interval for reconciling every configured repository.")
	fs.DurationVar(&o.maxConfigStaleness, "max-config-staleness", 15*time.Minute, "Config reload age after which readiness fails.")
	fs.DurationVar(&o.shutdownGracePeriod, "shutdown-grace-period", 30*time.Second, "Maximum graceful shutdown duration.")
	o.github.AddCustomizedFlags(fs, flagutil.ThrottlerDefaults(18000, 10))
	o.githubEventServer.Bind(fs)
	o.instrumentation.AddFlags(fs)
	_ = fs.Parse(os.Args[1:])
	return o
}

func (o *options) validate() error {
	var errs []error
	if o.configDir == "" {
		errs = append(errs, errors.New("--config-dir is required"))
	}
	if o.forwardingConfigPath == "" {
		errs = append(errs, errors.New("--forwarding-config is required"))
	}
	if o.pluginConfigDir == "" {
		errs = append(errs, errors.New("--plugin-config-dir is required"))
	}
	usingToken := o.github.TokenPath != ""
	usingApp := o.github.AppID != "" || o.github.AppPrivateKeyPath != ""
	if usingToken == usingApp || (o.github.AppID == "") != (o.github.AppPrivateKeyPath == "") {
		errs = append(errs, errors.New("configure exactly one of --github-token-path or --github-app-id with --github-app-private-key-path"))
	}
	if err := o.github.Validate(false); err != nil {
		errs = append(errs, err)
	}
	if o.workers < 1 {
		errs = append(errs, errors.New("--workers must be positive"))
	}
	if o.maxRetries < 0 {
		errs = append(errs, errors.New("--max-retries must not be negative"))
	}
	if o.configReload <= 0 || o.fullResync <= 0 || o.retryExhaustedDelay <= 0 {
		errs = append(errs, errors.New("periods and timeouts must be positive"))
	}
	if o.shutdownGracePeriod <= 0 {
		errs = append(errs, errors.New("shutdown grace period must be positive"))
	}
	if o.maxConfigStaleness <= o.configReload {
		errs = append(errs, errors.New("config staleness must exceed reload period"))
	}
	if err := o.instrumentation.Validate(false); err != nil {
		errs = append(errs, err)
	}
	if err := o.githubEventServer.DefaultAndValidate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func main() {
	logrusutil.ComponentInit()
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	if err := secret.Add(o.hmacPath); err != nil {
		logrus.WithError(err).Fatal("start HMAC secret agent")
	}
	if raw, err := os.ReadFile(o.hmacPath); err != nil || len(raw) == 0 {
		logrus.WithError(err).Fatal("webhook HMAC secret is missing or empty")
	}
	healthState := &runtimeHealth{maxConfigStaleness: o.maxConfigStaleness}
	githubClient, err := o.github.GitHubClientWithLogFields(false, logrus.Fields{"component": componentName})
	if err != nil {
		logrus.WithError(err).Fatal("create GitHub client")
	}
	refs := newGitHubRefClient(githubClient, healthState)

	state := newDesiredState()
	if _, err := githubClient.BotUser(); err != nil {
		logrus.WithError(err).Fatal("validate GitHub credentials")
	}
	healthState.githubSucceeded()
	controller := newController(refs, state, o.maxRetries, o.retryExhaustedDelay)
	ctx := interrupts.Context()
	reload := func() {
		forwardingConfig, err := loadForwardingConfig(o.forwardingConfigPath)
		if err != nil {
			logrus.WithError(err).Error("reload forwarding configuration")
			return
		}
		next, err := loadDesiredState(o.configDir, forwardingConfig)
		if err != nil {
			logrus.WithError(err).Error("reload desired state")
			return
		}
		if err := validateExternalPluginRegistrations(o.pluginConfigDir, next); err != nil {
			if !shouldContinueAfterPluginRegistrationError(err) {
				logrus.WithError(err).Error("validate external-plugin coverage")
				return
			}
			logrus.WithError(err).Warn("external-plugin coverage is incomplete; continuing with desired-state reload")
		}
		changed := state.replace(next)
		controller.enqueue(changed...)
		healthState.configSucceeded()
		logrus.WithFields(logrus.Fields{"repositories": len(next), "changed": len(changed)}).Info("loaded desired state")
	}
	reload()
	if len(state.keys()) == 0 {
		logrus.Warn("desired state is empty; waiting for configuration reload")
	}

	for i := 0; i < o.workers; i++ {
		go controller.runWorker(ctx)
	}
	go runPeriodic(ctx, o.configReload, reload)
	go runPeriodicJittered(ctx, o.fullResync, 0.1, func() {
		controller.enqueue(state.keys()...)
		lastFullResync.SetToCurrentTime()
	})

	metrics.ExposeMetrics(componentName, prowconfig.PushGateway{}, o.instrumentation.MetricsPort)
	pprofutil.Instrument(o.instrumentation)
	health := pjutil.NewHealthOnPort(o.instrumentation.HealthPort)
	health.ServeReady(func() bool { return healthState.ready(time.Now()) })
	eventServer := githubeventserver.New(o.githubEventServer, secret.GetTokenGenerator(o.hmacPath), logrus.WithField("component", componentName))
	eventServer.RegisterPushEventHandler((&pushEventHandler{state: state, controller: controller}).handle)
	interrupts.OnInterrupt(func() {
		healthState.serverHealthy.Store(false)
		controller.queue.ShutDown()
		eventServer.GracefulShutdown()
	})
	healthState.serverHealthy.Store(true)
	interrupts.ListenAndServe(eventServer, o.shutdownGracePeriod)
	interrupts.WaitForGracefulShutdown()
}

func runPeriodic(ctx context.Context, interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}

func runPeriodicJittered(ctx context.Context, interval time.Duration, factor float64, fn func()) {
	for {
		timer := time.NewTimer(jitterDuration(interval, factor))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			fn()
		}
	}
}

func shouldContinueAfterPluginRegistrationError(err error) bool {
	var missing missingExternalPluginRegistrationError
	return errors.As(err, &missing)
}
