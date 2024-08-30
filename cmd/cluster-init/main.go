package main

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	onboardcmd "github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/provision"
)

func main() {
	log := logrus.NewEntry(logrus.StandardLogger())
	ctx := handleSignals(signals.SetupSignalHandler(), log)

	root, err := newRootCmd(ctx, log)
	if err != nil {
		logrus.Fatalf("create root cmd: %s", err)
	}

	if err := root.Execute(); err != nil {
		logrus.Fatalf("%s", err)
	}
}

func newRootCmd(ctx context.Context, log *logrus.Entry) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "cluster-init",
		Short: "cluster-init manages a TP cluster lifecycle",
		Long:  "A tool to provision, onboard and deprovision a TP cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(onboardcmd.NewOnboard())
	provisionCmd, err := provision.NewProvision(ctx, log)
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(provisionCmd)

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
