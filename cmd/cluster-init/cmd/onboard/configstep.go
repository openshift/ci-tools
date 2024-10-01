package onboard

import (
	"context"
	"fmt"

	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/sirupsen/logrus"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

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
