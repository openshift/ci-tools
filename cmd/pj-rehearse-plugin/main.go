package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/flagutil"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/logrusutil"

	imagev1 "github.com/openshift/api/image/v1"
)

type options struct {
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

	dryRun          bool
	releaseRepoPath string

	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	git                      prowflagutil.GitOptions
}

func gatherOptions() options {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

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

	//The following fields are only surfaced for integration testing
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow. Only used for integration testing.")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested. Only used for integration testing.")

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

	if o.dryRun {
		if o.releaseRepoPath == "" {
			errs = append(errs, errors.New("candidate-path must be supplied when in dry-run mode"))
		}
	}

	errs = append(errs, o.githubEventServerOptions.DefaultAndValidate())
	errs = append(errs, o.github.Validate(o.dryRun))
	errs = append(errs, o.kubernetesOptions.Validate(o.dryRun))

	return utilerrors.NewAggregate(errs)
}

const (
	PrEnv = "PR"
)

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

	if o.dryRun {
		rc := rehearsalConfigFromOptions(o)
		prEnv, ok := os.LookupEnv(PrEnv)
		if !ok {
			logrus.Fatalf("couldn't get PR from env")
		}
		pr := &github.PullRequest{}
		if err := json.Unmarshal([]byte(prEnv), pr); err != nil {
			logrus.WithError(err).Fatal("couldn't unmarshall PR")
		}

		presubmits, periodics, changedTemplates, changedClusterProfiles := rc.determineAffectedJobs(*pr, o.releaseRepoPath, logger)
		if err := rc.rehearseJobs(*pr, o.releaseRepoPath, presubmits, periodics, changedTemplates, changedClusterProfiles, o.moreLimit, logger); err != nil {
			logrus.WithError(err).Fatal("failed to rehearse jobs")
		}
	}
}
