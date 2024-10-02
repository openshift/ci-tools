package config

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

type updateConfigOptions struct {
	prowflagutil.KubernetesOptions
	releaseRepo       string
	clusterInstallDir string
}

func (o *updateConfigOptions) complete() {
	if o.clusterInstallDir == "" {
		o.clusterInstallDir = onboard.ClusterInstallPath(o.releaseRepo)
	}
}

func newUpdateCmd(log *logrus.Entry) (*cobra.Command, error) {
	opts := updateConfigOptions{}
	cmd := cobra.Command{
		Use:   "update",
		Short: "Update the configuration files for a set of clusters",
		Long:  "Update the configuration files for a set of clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete()
			return updateConfig(cmd.Context(), log, &opts)
		},
	}

	stdFs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.KubernetesOptions.NOInClusterConfigDefault = true
	opts.KubernetesOptions.AddFlags(stdFs)
	pf := cmd.PersistentFlags()
	pf.StringVar(&opts.releaseRepo, "release-repo", "", "Path to openshift/release.")
	if err := cmd.MarkPersistentFlagRequired("release-repo"); err != nil {
		return nil, err
	}
	pf.StringVar(&opts.clusterInstallDir, "cluster-install-dir", "", "Path to the directory containing cluster install files.")
	pf.AddGoFlagSet(stdFs)

	return &cmd, nil
}

func updateConfig(ctx context.Context, log *logrus.Entry, opts *updateConfigOptions) error {
	kubeconfigs, err := opts.KubernetesOptions.LoadClusterConfigs()
	if err != nil {
		return fmt.Errorf("load kubeconfigs: %w", err)
	}
	newKubeClient := func(kubeconfigs map[string]rest.Config, clusterName string) (ctrlruntimeclient.Client, error) {
		config, found := kubeconfigs[clusterName]
		if !found {
			return nil, fmt.Errorf("kubeconfig for %s not found", clusterName)
		}
		return ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
	}

	clusterInstalls, err := clusterinstall.LoadFromDir(opts.clusterInstallDir,
		clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{ReleaseRepo: opts.releaseRepo}))
	if err != nil {
		return fmt.Errorf("load cluster-installs: %w", err)
	}

	for clusterName, clusterInstall := range clusterInstalls {
		kubeClient, err := newKubeClient(kubeconfigs, clusterName)
		if err != nil {
			return fmt.Errorf("new kubeclient for %s: %w", clusterName, err)
		}
		if err := runConfigSteps(ctx, log, true, kubeClient, clusterInstall); err != nil {
			return fmt.Errorf("update config for cluster %s: %w", clusterName, err)
		}
	}

	return nil
}
