package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	pprofutil "k8s.io/test-infra/prow/pjutil/pprof"

	imagev1 "github.com/openshift/api/image/v1"

	quayiociimagesdistributor "github.com/openshift/ci-tools/pkg/controller/quay_io_ci_images_distributor"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

type options struct {
	logLevel               string
	instrumentationOptions prowflagutil.InstrumentationOptions

	prowjobKubeconfig string
	kubernetesOptions prowflagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	normalLimit int
	moreLimit   int
	maxLimit    int

	gcsBucket          string
	gcsCredentialsFile string
	gcsBrowserPrefix   string

	dryRun        bool
	dryRunOptions dryRunOptions

	stickyLabelAuthors prowflagutil.Strings

	webhookSecretFile        string
	registryConfig           string
	ciImagesMirrorConfigPath string
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
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")

	fs.IntVar(&o.normalLimit, "normal-limit", 10, "Upper limit of jobs attempted to rehearse with normal command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.moreLimit, "more-limit", 20, "Upper limit of jobs attempted to rehearse with more command (if more jobs are being touched, only this many will be rehearsed)")
	fs.IntVar(&o.maxLimit, "max-limit", 35, "Upper limit of jobs attempted to rehearse with max command (if more jobs are being touched, only this many will be rehearsed)")

	fs.Var(&o.stickyLabelAuthors, "sticky-label-author", "PR Author for which the 'rehearsals-ack' label will not be removed upon a new push. Can be passed multiple times.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.registryConfig, "registry-config", "", "Path to the file of registry credentials")
	fs.StringVar(&o.ciImagesMirrorConfigPath, "ci-images-mirror-config-path", "", "Path to ci-image-mirror config path file")

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
		NoTemplates:        o.noTemplates,
		NoRegistry:         o.noRegistry,
		NoClusterProfiles:  o.noClusterProfiles,
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

	presubmits, periodics, changedTemplates, changedClusterProfiles, err := rc.DetermineAffectedJobs(candidate, candidatePath, logger)
	if err != nil {
		return fmt.Errorf("error determining affected jobs: %w: %s", err, "ERROR: pj-rehearse: misconfiguration")
	}

	prConfig, prRefs, imageStreamTags, presubmitsToRehearse, err := rc.SetupJobs(candidate, candidatePath, presubmits, periodics, changedTemplates, changedClusterProfiles, dro.limit, logger)
	if err != nil {
		return fmt.Errorf("error setting up jobs: %w: %s", err, "ERROR: pj-rehearse: setup failure")
	}

	if len(presubmitsToRehearse) > 0 {
		if err := prConfig.Prow.ValidateJobConfig(); err != nil {
			return fmt.Errorf("%s: %w", "ERROR: pj-rehearse: failed to validate rehearsal jobs", err)
		}

		_, err := rc.RehearseJobs(candidate, candidatePath, prRefs, imageStreamTags, quayiociimagesdistributor.OCImageMirrorOptions{}, nil, nil, presubmitsToRehearse, changedTemplates, changedClusterProfiles, prConfig.Prow, true, logger)
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

		if err = secret.Add(o.github.TokenPath, o.webhookSecretFile); err != nil {
			logger.WithError(err).Fatal("Error starting secrets agent.")
		}
		webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)

		s, err := serverFromOptions(o)
		if err != nil {
			logger.WithError(err).Fatal("couldn't create server")
		}

		logger.Debug("starting eventServer")
		eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
		eventServer.RegisterHandlePullRequestEvent(s.handlePullRequestCreation)
		eventServer.RegisterHandlePullRequestEvent(s.handleNewPush)
		eventServer.RegisterHandleIssueCommentEvent(s.handleIssueComment)
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
