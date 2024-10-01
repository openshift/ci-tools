package config

import (
	"context"
	"fmt"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewCmd(ctx context.Context, log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "config",
		Short: "Handle configurations for a cluster",
		Long:  "Generate and apply configurations for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	generateConfigCmd, err := newGenerateCmd(ctx, log, opts)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	cmd.AddCommand(generateConfigCmd)

	updateConfigCmd, err := newUpdateCmd(ctx, log)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	cmd.AddCommand(updateConfigCmd)

	cmd.AddCommand(newApplyCmd(ctx, log, opts))
	return &cmd, nil
}

func runConfigSteps(ctx context.Context, log *logrus.Entry, update bool, kubeClient ctrlruntimeclient.Client, clusterInstall *clusterinstall.ClusterInstall) error {
	steps := []clusterinit.Step{
		onboard.NewProwJobStep(log, clusterInstall),
		onboard.NewBuildClusterDirStep(log, clusterInstall),
		onboard.NewOAuthTemplateStep(log, clusterInstall),
		onboard.NewCiSecretBootstrapStep(log, clusterInstall),
		onboard.NewCiSecretGeneratorStep(log, clusterInstall),
		onboard.NewSanitizeProwjobStep(log, clusterInstall),
		onboard.NewSyncRoverGroupStep(log, clusterInstall),
		onboard.NewProwPluginStep(log, clusterInstall),
		onboard.NewDexStep(log, kubeClient, clusterInstall),
		onboard.NewQuayioPullThroughCacheStep(log, clusterInstall, kubeClient),
		onboard.NewCertificateStep(log, clusterInstall, kubeClient),
	}
	if !update {
		steps = append(steps, onboard.NewBuildClusterStep(log, clusterInstall))
	}

	for _, step := range steps {
		if err := step.Run(ctx); err != nil {
			return fmt.Errorf("run config step %s: %w", step.Name(), err)
		}
	}
	return nil
}
