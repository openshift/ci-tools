package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"

	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

// TODO: for now keeping all the options, determine if this is necessary
type options struct {
	dryRun   bool
	logLevel string

	debugLogPath      string
	prowjobKubeconfig string
	kubernetesOptions flagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool
	preCheck          bool

	normalLimit int
	moreLimit   int
	maxLimit    int

	webhookSecretFile string

	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	git                      prowflagutil.GitOptions
}

func gatherOptions() options {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr") //TODO: not sure this one makes sense anymore
	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")
	fs.BoolVar(&o.preCheck, "pre-check", false, "If true, check for rehearsable jobs and provide the list upon PR creation")

	fs.IntVar(&o.normalLimit, "normal-limit", 10, "Upper limit of jobs attempted to rehearse with normal command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.moreLimit, "more-limit", 20, "Upper limit of jobs attempted to rehearse with more command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.maxLimit, "max-limit", 35, "Upper limit of jobs attempted to rehearse with max command (if more jobs are being touched, only this many will be rehearsed)")

	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}

	return o
}

func (o *options) validate() error {
	var errs []error
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		errs = append(errs, fmt.Errorf("invalid log level specified: %w", err))
	}
	logrus.SetLevel(level)

	errs = append(errs, o.githubEventServerOptions.DefaultAndValidate())
	errs = append(errs, o.github.Validate(o.dryRun))
	errs = append(errs, o.kubernetesOptions.Validate(o.dryRun))

	return utilerrors.NewAggregate(errs)
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", "pj-rehearse")

	o := gatherOptions()
	if err := o.validate(); err != nil {
		logger.Fatalf("Invalid options: %v", err)
	}

	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("failed to register imagev1 scheme")
	}

	if err := secret.Add(o.github.TokenPath, o.webhookSecretFile); err != nil {
		logger.WithError(err).Fatal("Error starting secrets agent.")
	}
	webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)

	server, err := serverFromOptions(o)
	if err != nil {
		logger.WithError(err).Fatal("couldn't create server")
	}

	logger.Debug("starting eventServer")
	eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
	eventServer.RegisterHandlePullRequestEvent(server.handlePullRequestCreation)
	eventServer.RegisterHandleIssueCommentEvent(server.handleIssueComment)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
