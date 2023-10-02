package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"

	buildv1 "github.com/openshift/api/build/v1"

	multiarchbuildconfigv1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/openshift/ci-tools/pkg/controller/multiarchbuildconfig"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

type options struct {
	dryRun        bool
	dockerCfgPath string
	kubernetes    prowflagutil.KubernetesOptions
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.StringVar(&o.dockerCfgPath, "docker-cfg", "/.docker/config.json", "Path of the registry credentials configuration file")

	o.kubernetes.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func main() {
	logrusutil.ComponentInit()
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))

	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}

	ctx := controllerruntime.SetupSignalHandler()

	cfg, err := o.kubernetes.InfrastructureClusterConfig(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}

	kubeClient, err := coreclientset.NewForConfig(cfg)
	if err != nil {
		logrus.WithError(err).Fatal("could not get core client for cluster config")
	}

	mgr, err := controllerruntime.NewManager(cfg, controllerruntime.Options{
		DryRunClient: o.dryRun,
	})
	if err != nil {
		logrus.WithError(err).Fatal("failed to construct manager")
	}

	if err := multiarchbuildconfigv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add multiarchbuildconfig to scheme")
	}

	if err := buildv1.AddToScheme(mgr.GetScheme()); err != nil {
		logrus.WithError(err).Fatal("Failed to add multiarchbuildconfig to scheme")
	}

	nodeArchitectures, err := resolveNodeArchitectures(ctx, kubeClient.Nodes())
	if err != nil {
		logrus.WithError(err).Fatal("failed to retrieve the node architectures")
	}

	if err := multiarchbuildconfig.AddToManager(mgr, nodeArchitectures, o.dockerCfgPath); err != nil {
		logrus.WithError(err).Fatal("Failed to add multiarchbuildconfig controller to manager")
	}

	if err := mgr.Start(ctx); err != nil {
		logrus.WithError(err).Fatal("Manager ended with error")
	}

	logrus.Info("Process ended gracefully")
}

func resolveNodeArchitectures(ctx context.Context, client coreclientset.NodeInterface) ([]string, error) {
	ret := sets.New[string]()

	// TODO(@droslean):
	// This approach won't ensure to include an architecture from a scaled-down to 0 machineset.
	// When we will start adding more architectures to the heterogeneous cluster, we will need to
	// make sure that at least one node is running or change this approach to gather the architectures
	// from the machinessets which is really hard now.
	nodeList, err := client.List(ctx, metav1.ListOptions{})

	if err != nil {
		return nil, fmt.Errorf("failed to determine the node architectures: %w", err)
	}

	for _, node := range nodeList.Items {
		ret.Insert(node.Status.NodeInfo.Architecture)
	}
	return sets.List(ret), nil
}
