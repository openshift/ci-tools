package main

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/version"

	imagev1 "github.com/openshift/api/image/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	routev1 "github.com/openshift/api/route/v1"

	onboardcmd "github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/provision"
	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
)

func main() {
	log := initLog()
	ctx := handleSignals(signals.SetupSignalHandler(), log)

	if err := addSchemes(); err != nil {
		log.Fatalf("%s", err)
	}

	root, err := newRootCmd(log)
	if err != nil {
		log.Fatalf("create root cmd: %s", err)
	}

	if err := root.ExecuteContext(ctx); err != nil {
		log.Fatalf("%s", err)
	}
}

func newRootCmd(log *logrus.Entry) (*cobra.Command, error) {
	opts := &runtime.Options{}
	cmd := cobra.Command{
		Use:   "cluster-init",
		Short: "cluster-init manages a TP cluster lifecycle",
		Long:  "A tool to provision, onboard and deprovision a TP cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&opts.ClusterInstall, "cluster-install", "", "Path to cluster-install.yaml")
	cmd.PersistentFlags().StringVar(&opts.InstallBase, "install-base", "", "The working directory in which artifacts will be dropped.")
	onboardCmd, err := onboardcmd.NewOnboard(log, opts)
	if err != nil {
		return nil, fmt.Errorf("onboard: %w", err)
	}
	cmd.AddCommand(onboardCmd)
	provisionCmd, err := provision.NewProvision(log, opts)
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(provisionCmd)
	cmd.SilenceUsage = true

	return &cmd, nil
}

func handleSignals(signalCtx context.Context, log *logrus.Entry) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-signalCtx.Done()
		log.Warn("Received interrupt signal")
		cancel()
	}()
	return ctx
}

func initLog() *logrus.Entry {
	logrusutil.Init(&logrusutil.DefaultFieldsFormatter{
		PrintLineNumber:  true,
		DefaultFields:    logrus.Fields{"component": version.Name},
		WrappedFormatter: &logrus.TextFormatter{},
	})
	return logrus.NewEntry(logrus.StandardLogger())
}

func addSchemes() error {
	if err := routev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("add routev1 to scheme: %w", err)
	}
	if err := imageregistryv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("add imageregistryv1 to scheme: %w", err)
	}
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("add imagev1 to scheme: %w", err)
	}
	return nil
}
