package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bombsimon/logrusr"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/rest"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"

	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/controller/prpqr_reconciler"
)

type options struct {
	namespace string
	dryRun    bool
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.StringVar(&o.namespace, "namespace", "ci", "In which namespace the operation will take place")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func main() {
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.NewLogger(logrus.StandardLogger()))

	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}

	ctx := controllerruntime.SetupSignalHandler()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}

	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		DryRunClient: o.dryRun,
		Logger:       ctrlruntimelog.NullLogger{},
	})
	if err != nil {
		logrus.WithError(err).Fatal("failed to construct manager")
	}

	if err := prowv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add prowv1 to scheme")
	}

	if err := prpqv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add prpqv1 to scheme")
	}

	if err := prpqr_reconciler.AddToManager(mgr, o.namespace); err != nil {
		logrus.WithError(err).Fatal("Failed to add prpqr_reconciler to manager")
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
