package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	rhcostream "github.com/coreos/stream-metadata-go/stream"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	installertypes "github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	awsruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/aws"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard/certmanager"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard/cischedulingwebhook"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard/machineset"
	clusterinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
	"github.com/openshift/ci-tools/pkg/kubernetes/portforward"
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

	return &cmd, nil
}

func runConfigSteps(ctx context.Context, log *logrus.Entry, update bool, clusterInstall *clusterinstall.ClusterInstall,
	ctrlClient ctrlruntimeclient.Client, kubeClient *kubernetes.Clientset) error {
	steps := []clusterinittypes.Step{
		onboard.NewProwJobStep(log, clusterInstall),
		onboard.NewBuildClusterDirStep(log, clusterInstall),
		onboard.NewManifestGeneratorStep(log, onboard.NewOAuthTemplateGenerator(clusterInstall)),
		onboard.NewCISecretBootstrapStep(log, clusterInstall),
		onboard.NewCISecretGeneratorStep(log, clusterInstall),
		onboard.NewSanitizeProwjobStep(log, clusterInstall),
		onboard.NewSyncRoverGroupStep(log, clusterInstall),
		onboard.NewProwPluginStep(log, clusterInstall),
		onboard.NewManifestGeneratorStep(log, onboard.NewDexGenerator(ctrlClient, clusterInstall)),
		onboard.NewManifestGeneratorStep(log, onboard.NewQuayioPullThroughCacheStep(clusterInstall, ctrlClient)),
		onboard.NewManifestGeneratorStep(log, onboard.NewCertificateGenerator(clusterInstall, ctrlClient)),
		onboard.NewManifestGeneratorStep(log, onboard.NewCloudabilityAgentGenerator(clusterInstall)),
		onboard.NewCommonSymlinkStep(log, clusterInstall),
		onboard.NewManifestGeneratorStep(log, onboard.NewMultiarchBuilderControllerGenerator(clusterInstall)),
		onboard.NewMultiarchTuningOperatorStep(log, clusterInstall),
		onboard.NewManifestGeneratorStep(log, onboard.NewImageRegistryGenerator(clusterInstall)),
		onboard.NewManifestGeneratorStep(log, onboard.NewOpenshiftMonitoringGenerator(clusterInstall)),
		onboard.NewPassthroughStep(log, clusterInstall),
	}

	steps = addCloudSpecificSteps(log, update, ctrlClient, kubeClient, steps, clusterInstall)
	if !update {
		steps = append(steps, onboard.NewBuildClusterStep(log, clusterInstall))
		steps = append(steps, onboard.NewManifestGeneratorStep(log, certmanager.NewGenerator(clusterInstall, ctrlClient, portforward.SPDYPortForwarder, grpc.NewClient)))
	}

	for _, step := range steps {
		if err := step.Run(ctx); err != nil {
			return fmt.Errorf("run config step %s: %w", step.Name(), err)
		}
	}
	return nil
}

func addCloudSpecificSteps(log *logrus.Entry, update bool, ctrlClient ctrlruntimeclient.Client, kubeClient *kubernetes.Clientset, steps []clusterinittypes.Step, clusterInstall *clusterinstall.ClusterInstall) []clusterinittypes.Step {
	if clusterInstall.Provision.AWS != nil {
		var configGetter awsruntime.ConfigGetter
		if update {
			configGetter = awsruntime.ConfigFromCluster(kubeClient, ctrlClient)
		} else {
			configGetter = awsruntime.ConfigFromDefaults()
		}
		awsProvider := awsruntime.NewProvider(clusterInstall, configGetter)
		steps = append(steps, onboard.NewManifestGeneratorStep(log, cischedulingwebhook.NewGenerator(clusterInstall, cischedulingwebhook.NewAWSProvider(awsProvider))))
		steps = append(steps, onboard.NewManifestGeneratorStep(log, machineset.NewGenerator(clusterInstall, machineset.NewAWSProvider(awsProvider))))
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
		return fmt.Errorf("get kube-system/cluster-config-v1: %w", err)
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

	cm = corev1.ConfigMap{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: "openshift-machine-config-operator", Name: "coreos-bootimages"}, &cm); err != nil {
		return fmt.Errorf("get openshift-machine-config-operator/coreos-bootimages: %w", err)
	}
	if _, ok := cm.Data["stream"]; !ok {
		return errors.New("coreos stream data not found")
	}
	stream := rhcostream.Stream{}
	if err := json.Unmarshal([]byte(cm.Data["stream"]), &stream); err != nil {
		return fmt.Errorf("unmarshal coreos stream: %w", err)
	}
	ci.CoreOSStream = stream

	return nil
}
