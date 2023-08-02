package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	imagev1 "github.com/openshift/api/image/v1"

	quayiociimagesdistributor "github.com/openshift/ci-tools/pkg/controller/quay_io_ci_images_distributor"
)

var allControllers = sets.New[string](
	quayiociimagesdistributor.ControllerName,
)

type options struct {
	leaderElectionNamespace          string
	leaderElectionSuffix             string
	enabledControllers               flagutil.Strings
	enabledControllersSet            sets.Set[string]
	dryRun                           bool
	quayIOCIImagesDistributorOptions quayIOCIImagesDistributorOptions
}

func (o *options) addDefaults() {
	o.enabledControllers = flagutil.NewStrings(quayiociimagesdistributor.ControllerName)
}

type quayIOCIImagesDistributorOptions struct {
	additionalImageStreamNamespacesRaw flagutil.Strings
	additionalImageStreamNamespaces    sets.Set[string]
}

func newOpts() *options {
	opts := &options{}
	opts.addDefaults()
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&opts.leaderElectionNamespace, "leader-election-namespace", "ci", "The namespace to use for leader election")
	fs.StringVar(&opts.leaderElectionSuffix, "leader-election-suffix", "", "Suffix for the leader election lock. Useful for local testing. If set, --dry-run must be set as well")
	fs.Var(&opts.enabledControllers, "enable-controller", fmt.Sprintf("Enabled controllers. Available controllers are: %v. Can be specified multiple times. Defaults to %v", allControllers.UnsortedList(), opts.enabledControllers.Strings()))
	fs.BoolVar(&opts.dryRun, "dry-run", false, "Whether to run the controller-manager with dry-run")
	fs.Var(&opts.quayIOCIImagesDistributorOptions.additionalImageStreamNamespacesRaw, "quayIOCIImagesDistributorOptions.additional-image-stream-namespace", "A namespace in which imagestreams will be distributed even if no test explicitly references them (e.G `ci`). Can be passed multiple times.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	var errs []error
	if o.leaderElectionNamespace == "" {
		errs = append(errs, errors.New("--leader-election-namespace must be set"))
	}
	if o.leaderElectionSuffix != "" && !o.dryRun {
		errs = append(errs, errors.New("dry-run must be set if --leader-election-suffix is set"))
	}
	if values := o.enabledControllers.Strings(); len(values) > 0 {
		o.enabledControllersSet = sets.New[string](values...)
		if diff := o.enabledControllersSet.Difference(allControllers); diff.Len() > 0 {
			errs = append(errs, fmt.Errorf("the following controllers are unknown: %v", diff.UnsortedList()))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func main() {
	logrusutil.ComponentInit()
	opts := newOpts()
	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate options")
	}

	ctx := controllerruntime.SetupSignalHandler()

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}

	clientOptions := ctrlruntimeclient.Options{}
	clientOptions.DryRun = &opts.dryRun
	mgr, err := controllerruntime.NewManager(inClusterConfig, controllerruntime.Options{
		Client:                        clientOptions,
		LeaderElection:                true,
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.leaderElectionNamespace,
		LeaderElectionID:              fmt.Sprintf("ci-image-mirror%s", opts.leaderElectionSuffix),
	})

	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct manager for the hive cluster")
	}

	if err := imagev1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 to scheme")
	}
	// The image api is implemented via the Openshift Extension APIServer, so contrary
	// to CRD-Based resources it supports protobuf.
	if err := apiutil.AddToProtobufScheme(imagev1.AddToScheme); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 api to protobuf scheme")
	}

	if opts.enabledControllersSet.Has(quayiociimagesdistributor.ControllerName) {
		if err := quayiociimagesdistributor.AddToManager(mgr, opts.quayIOCIImagesDistributorOptions.additionalImageStreamNamespaces); err != nil {
			logrus.WithField("name", quayiociimagesdistributor.ControllerName).WithError(err).Fatal("Failed to construct the controller")
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}
