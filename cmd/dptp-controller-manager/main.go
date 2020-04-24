package main

import (
	"errors"
	"flag"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/git"
	"k8s.io/test-infra/prow/pjutil"
	"sigs.k8s.io/controller-runtime"

	"github.com/openshift/ci-tools/pkg/controller/image-stream-tag-reconciler"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

type options struct {
	LeaderElectionNamespace string
	CiOperatorConfigPath    string
	ProwJobNamespace        string
	DryRun                  bool
}

func newOpts() (*options, error) {
	opts := &options{}
	flag.StringVar(&opts.LeaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leaderelection")
	flag.StringVar(&opts.CiOperatorConfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	flag.StringVar(&opts.ProwJobNamespace, "prow-job-namespace", "ci", "Namespace to create prowjobs in")
	// TODO: rather than relying on humans implementing dry-run properly, we should switch
	// to just do it on client-level once it becomes available: https://github.com/kubernetes-sigs/controller-runtime/pull/839
	flag.BoolVar(&opts.DryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	flag.Parse()

	var errs []error
	if opts.LeaderElectionNamespace == "" {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if opts.CiOperatorConfigPath == "" {
		errs = append(errs, errors.New("--ci-operations-config-path must be set"))
	}
	if opts.ProwJobNamespace == "" {
		errs = append(errs, errors.New("--prow-job-namespace must be set"))
	}

	return opts, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := newOpts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}
	logrus.SetLevel(logrus.InfoLevel)

	cfg, err := controllerruntime.GetConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get kubeconfig")
	}

	ciOPConfigAgent, err := agents.NewConfigAgent(opts.CiOperatorConfigPath, 2*time.Minute, prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"error"}))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct ci-opeartor config agent")
	}
	gitClient, err := git.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct git client")
	}
	// TODO alvaroaleman: Fix upstream, needed because otherwise we get a NPD
	gitClient.SetCredentials("", func() []byte { return nil })

	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		LeaderElection:          true,
		LeaderElectionNamespace: opts.LeaderElectionNamespace,
		LeaderElectionID:        "dptp-controller-manager",
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager")
	}
	pjutil.ServePProf()

	imageStreamTagReconcilerOpts := imagestreamtagreconciler.Options{
		DryRun:                opts.DryRun,
		CIOperatorConfigAgent: ciOPConfigAgent,
		ProwJobNamespace:      opts.ProwJobNamespace,
		GitClient:             gitClient,
	}
	if err := imagestreamtagreconciler.AddToManager(mgr, imageStreamTagReconcilerOpts); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagestreamtagreconciler")
	}

	stopCh := controllerruntime.SetupSignalHandler()
	if err := mgr.Start(stopCh); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
