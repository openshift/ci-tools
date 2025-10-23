package config

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"

	kuberuntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/kube"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

type updateConfigOptions struct {
	prowflagutil.KubernetesOptions
	releaseRepo       string
	releaseBranch     string
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
	pf.StringVar(&opts.releaseBranch, "release-branch", "main", "Release branch name to be used.")
	pf.AddGoFlagSet(stdFs)

	return &cmd, nil
}

func updateConfig(ctx context.Context, log *logrus.Entry, opts *updateConfigOptions) error {
	kubeconfigs, err := opts.KubernetesOptions.LoadClusterConfigs()
	if err != nil {
		return fmt.Errorf("load kubeconfigs: %w", err)
	}
	newKubeClients := func(kubeconfigs map[string]rest.Config, clusterName string) (ctrlruntimeclient.Client, *kubernetes.Clientset, *rest.Config, error) {
		var kubeClient *kubernetes.Clientset
		config, found := kubeconfigs[clusterName]
		if !found {
			return nil, kubeClient, nil, fmt.Errorf("kubeconfig for %s not found", clusterName)
		}

		client, err := kuberuntime.NewClient(&config)
		if err != nil {
			return nil, kubeClient, nil, fmt.Errorf("new ctrl client: %w", err)
		}

		kubeClient, err = kubernetes.NewForConfig(&config)
		if err != nil {
			return nil, kubeClient, nil, fmt.Errorf("new client: %w", err)
		}

		return client, kubeClient, &config, err
	}

	clusterInstalls, err := clusterinstall.LoadFromDir(opts.clusterInstallDir,
		clusterinstall.FinalizeOption(clusterinstall.FinalizeOptions{ReleaseRepo: opts.releaseRepo}))
	if err != nil {
		return fmt.Errorf("load cluster-installs: %w", err)
	}

	for clusterName, clusterInstall := range clusterInstalls {
		ctrlClient, kubeClient, config, err := newKubeClients(kubeconfigs, clusterName)
		clusterInstall.Config = config
		if err != nil {
			log.WithField("cluster", clusterName).WithError(err).Warn("Skipping cluster due to missing or invalid kubeconfig")
			continue
		}
		if err := addClusterInstallRuntimeInfo(ctx, clusterInstall, ctrlClient); err != nil {
			return err
		}
		if err := runConfigSteps(ctx, log, true, clusterInstall, ctrlClient, kubeClient, opts.releaseBranch); err != nil {
			return fmt.Errorf("update config for cluster %s: %w", clusterName, err)
		}
	}

	return nil
}
