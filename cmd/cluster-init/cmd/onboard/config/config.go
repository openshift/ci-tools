package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	installertypes "github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	awsruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/aws"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard/cischedulingwebhook"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard/machineset"
	clusterinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

func NewCmd(log *logrus.Entry, opts *runtime.Options) (*cobra.Command, error) {
	cmd := cobra.Command{
		Use:   "config",
		Short: "Handle configurations for a cluster",
		Long:  "Generate and apply configurations for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	generateConfigCmd, err := newGenerateCmd(log, opts)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	cmd.AddCommand(generateConfigCmd)

	updateConfigCmd, err := newUpdateCmd(log)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	cmd.AddCommand(updateConfigCmd)

	applyCmd, err := newApplyCmd(log, opts)
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	cmd.AddCommand(applyCmd)
	return &cmd, nil
}

func runConfigSteps(ctx context.Context, log *logrus.Entry, update bool, clusterInstall *clusterinstall.ClusterInstall,
	kubeClient ctrlruntimeclient.Client) error {
	steps := []clusterinittypes.Step{
		onboard.NewProwJobStep(log, clusterInstall),
		onboard.NewBuildClusterDirStep(log, clusterInstall),
		onboard.NewOAuthTemplateStep(log, clusterInstall),
		onboard.NewCISecretBootstrapStep(log, clusterInstall),
		onboard.NewCISecretGeneratorStep(log, clusterInstall),
		onboard.NewSanitizeProwjobStep(log, clusterInstall),
		onboard.NewSyncRoverGroupStep(log, clusterInstall),
		onboard.NewProwPluginStep(log, clusterInstall),
		onboard.NewDexStep(log, kubeClient, clusterInstall),
		onboard.NewQuayioPullThroughCacheStep(log, clusterInstall, kubeClient),
		onboard.NewCertificateStep(log, clusterInstall, kubeClient),
		onboard.NewCloudabilityAgentStep(log, clusterInstall),
		onboard.NewCommonSymlinkStep(log, clusterInstall),
	}

	steps = addCloudSpecificSteps(log, kubeClient, steps, clusterInstall)
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

func addCloudSpecificSteps(log *logrus.Entry, kubeClient ctrlruntimeclient.Client, steps []clusterinittypes.Step, clusterInstall *clusterinstall.ClusterInstall) []clusterinittypes.Step {
	if clusterInstall.Provision.AWS != nil {
		awsProvider := awsruntime.NewProvider(clusterInstall, kubeClient)
		steps = append(steps, cischedulingwebhook.NewStep(log, clusterInstall, cischedulingwebhook.NewAWSProvider(awsProvider)))
		steps = append(steps, machineset.NewStep(log, clusterInstall, machineset.NewAWSProvider(awsProvider)))
	}
	return steps
}

func addClusterInstallRuntimeInfo(ctx context.Context, ci *clusterinstall.ClusterInstall, kubeClient ctrlruntimeclient.Client) error {
	infra := configv1.Infrastructure{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: "", Name: "cluster"}, &infra); err != nil {
		return fmt.Errorf("get infrastructure: %w", err)
	}
	ci.Infrastructure = infra

	cm := corev1.ConfigMap{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: "kube-system", Name: "cluster-config-v1"}, &cm); err != nil {
		return fmt.Errorf("get cluster-config-v1: %w", err)
	}
	installConfigRaw, ok := cm.Data["install-config"]
	if !ok {
		return errors.New("install-config not found")
	}
	installConfig := installertypes.InstallConfig{}
	if err := yaml.Unmarshal([]byte(installConfigRaw), &installConfig); err != nil {
		return fmt.Errorf("unmarshall install config: %w", err)
	}
	ci.InstallConfig = installConfig

	return nil
}
