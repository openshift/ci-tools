package config

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	kuberuntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/kube"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

type generateConfigOptions struct {
	releaseRepo string
	*runtime.Options
}

func newGenerateCmd(log *logrus.Entry, parentOpts *runtime.Options) (*cobra.Command, error) {
	opts := generateConfigOptions{}
	opts.Options = parentOpts
	cmd := cobra.Command{
		Use:   "generate",
		Short: "Generate the configuration files for a cluster",
		Long:  "Generate the configuration files for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateConfig(cmd.Context(), log, opts)
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringVar(&opts.releaseRepo, "release-repo", "", "Path to openshift/release.")
	if err := cmd.MarkPersistentFlagRequired("release-repo"); err != nil {
		return nil, err
	}
	return &cmd, nil
}

func generateConfig(ctx context.Context, log *logrus.Entry, opts generateConfigOptions) error {
	log = log.WithField("stage", "onboard config")

	clusterInstall, err := clusterinstall.Load(opts.ClusterInstall, clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{
		InstallBase: opts.Options.InstallBase,
		ReleaseRepo: opts.releaseRepo,
	}))
	if err != nil {
		return fmt.Errorf("load cluster-install: %w", err)
	}

	adminKubeconfigPath := onboard.AdminKubeconfig(clusterInstall.InstallBase)
	config, err := clientcmd.BuildConfigFromFlags("", adminKubeconfigPath)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	kubeClient, err := kuberuntime.NewClient(config)
	if err != nil {
		return fmt.Errorf("new kubeclient: %w", err)
	}
	if err := addClusterInstallRuntimeInfo(ctx, clusterInstall, kubeClient); err != nil {
		return err
	}

	if err := runConfigSteps(ctx, log, false, clusterInstall, kubeClient); err != nil {
		return fmt.Errorf("generate config for cluster %s, %w", clusterInstall.ClusterName, err)
	}

	return nil
}
