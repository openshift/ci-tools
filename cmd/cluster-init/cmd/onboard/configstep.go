package onboard

import (
	"context"
	"fmt"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	clustermgmtonboard "github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/sirupsen/logrus"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func runConfigSteps(ctx context.Context, log *logrus.Entry, update bool, kubeClient ctrlruntimeclient.Client, clusterInstall *clusterinstall.ClusterInstall) error {
	steps := []clustermgmt.Step{
		clustermgmtonboard.NewProwJobStep(log, clusterInstall),
		clustermgmtonboard.NewBuildClusterDirStep(log, clusterInstall),
		clustermgmtonboard.NewOAuthTemplateStep(log, clusterInstall),
		clustermgmtonboard.NewCiSecretBootstrapStep(log, clusterInstall),
		clustermgmtonboard.NewCiSecretGeneratorStep(log, clusterInstall),
		clustermgmtonboard.NewSanitizeProwjobStep(log, clusterInstall),
		clustermgmtonboard.NewSyncRoverGroupStep(log, clusterInstall),
		clustermgmtonboard.NewProwPluginStep(log, clusterInstall),
		clustermgmtonboard.NewDexStep(log, kubeClient, clusterInstall),
		clustermgmtonboard.NewQuayioPullThroughCacheStep(log, clusterInstall, kubeClient),
		clustermgmtonboard.NewCertificateStep(log, clusterInstall, kubeClient),
	}
	if !update {
		steps = append(steps, clustermgmtonboard.NewBuildClusterStep(log, clusterInstall))
	}

	for _, step := range steps {
		if err := step.Run(ctx); err != nil {
			return fmt.Errorf("run config step %s: %w", step.Name(), err)
		}
	}
	return nil
}
