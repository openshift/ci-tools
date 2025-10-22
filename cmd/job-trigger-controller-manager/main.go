package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/rest"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfigflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/controller/prpqr_reconciler"
	"github.com/openshift/ci-tools/pkg/registry/server"
)

var (
	configResolverAddress = api.URLForService(api.ServiceConfig)
)

type options struct {
	prowconfigflagutil.ConfigOptions

	namespace                         string
	jobTriggerWaitInSeconds           int64
	defaultAggregatorJobTimeoutInHour int64
	defaultMultiRefJobTimeoutInHour   int64
	dispatcherAddress                 string
	dryRun                            bool
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	o.AddFlags(fs)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.StringVar(&o.namespace, "namespace", "ci", "In which namespace the operation will take place")
	fs.Int64Var(&o.jobTriggerWaitInSeconds, "job-trigger-wait-seconds", 20, "Amount of seconds to wait for job to trigger in order to update status")
	fs.Int64Var(&o.defaultAggregatorJobTimeoutInHour, "aggregator-job-timeout", 6, "Amount of hours to wait for job to timeout in order to update status")
	fs.Int64Var(&o.defaultMultiRefJobTimeoutInHour, "multi-ref-job-timeout", 6, "Amount of hours to wait for job to timeout in order to update status")
	fs.StringVar(&o.dispatcherAddress, "dispatcher-address", "http://prowjob-dispatcher.ci.svc.cluster.local:8080", "Address of prowjob-dispatcher server.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) Validate() error {
	return o.ConfigOptions.Validate(o.dryRun)
}

func main() {
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))

	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}

	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	agent, err := o.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("could not load Prow configuration")
	}

	ctx := controllerruntime.SetupSignalHandler()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}

	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		Client: client.Options{
			DryRun: &o.dryRun,
		},
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

	duration := time.Duration(o.jobTriggerWaitInSeconds) * time.Second
	defaultAggregatorJobTimeout := time.Duration(o.defaultAggregatorJobTimeoutInHour) * time.Hour
	defaultMultiRefJobTimeout := time.Duration(o.defaultMultiRefJobTimeoutInHour) * time.Hour
	if err := prpqr_reconciler.AddToManager(mgr, o.namespace, server.NewResolverClient(configResolverAddress), agent, o.dispatcherAddress, duration, defaultAggregatorJobTimeout, defaultMultiRefJobTimeout); err != nil {
		logrus.WithError(err).Fatal("Failed to add prpqr_reconciler to manager")
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
