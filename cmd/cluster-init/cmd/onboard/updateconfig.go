package onboard

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	clustermgmtonboard "github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
)

type updateConfigOptions struct {
	prowflagutil.KubernetesOptions
	releaseRepo       string
	clusterInstallDir string
}

func (o *updateConfigOptions) complete() {
	if o.clusterInstallDir == "" {
		o.clusterInstallDir = clustermgmtonboard.ClusterInstallPath(o.releaseRepo)
	}
}

func newUpdateConfigCmd(ctx context.Context, log *logrus.Entry) *cobra.Command {
	opts := updateConfigOptions{}
	cmd := cobra.Command{
		Use:   "update",
		Short: "Update the configuration files for a set of clusters",
		Long:  "Update the configuration files for a set of clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete()
			return updateConfig(ctx, log, &opts)
		},
	}

	stdFs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.KubernetesOptions.NOInClusterConfigDefault = true
	opts.KubernetesOptions.AddFlags(stdFs)
	pf := cmd.PersistentFlags()
	pf.StringVar(&opts.releaseRepo, "release-repo", "", "Path to openshift/release.")
	cmd.MarkPersistentFlagRequired("release-repo")
	pf.StringVar(&opts.clusterInstallDir, "cluster-install-dir", "", "Path to the directory containing cluster install files.")
	pf.AddGoFlagSet(stdFs)

	return &cmd
}

func loadClusterInstalls(opts *updateConfigOptions) (map[string]*clusterinstall.ClusterInstall, error) {
	clusterInstalls := make(map[string]*clusterinstall.ClusterInstall)
	filepath.WalkDir(opts.clusterInstallDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read dir %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}
		ci, err := clusterinstall.Load(path)
		if err != nil {
			return fmt.Errorf("load cluster-install %s: %w", path, err)
		}
		ci.Onboard.ReleaseRepo = opts.releaseRepo
		clusterInstalls[ci.ClusterName] = ci
		return nil
	})
	return clusterInstalls, nil
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

	clusterInstalls, err := loadClusterInstalls(opts)
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
