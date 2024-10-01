package onboard

import (
	"context"
	"fmt"

	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusterdir"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusters"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretbootstrap"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretgenerator"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/jobs"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/prowplugin"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/sanitizeprowjob"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/syncrovergroup"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	clustermgmtonboard "github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/sirupsen/logrus"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func runConfigSteps(ctx context.Context, log *logrus.Entry, update bool, kubeClient ctrlruntimeclient.Client, clusterInstall *clusterinstall.ClusterInstall) error {
	steps := []func() error{
		func() error { return jobs.UpdateJobs(log, clusterInstall) },
		func() error { return buildclusterdir.UpdateClusterBuildFarmDir(log, clusterInstall) },
		func() error { return clustermgmtonboard.NewOAuthTemplateStep(log, clusterInstall).Run(ctx) },
		func() error { return cisecretbootstrap.UpdateCiSecretBootstrap(log, clusterInstall) },
		func() error { return cisecretgenerator.UpdateSecretGenerator(log, clusterInstall) },
		func() error { return sanitizeprowjob.UpdateSanitizeProwJobs(log, clusterInstall) },
		func() error { return syncrovergroup.UpdateSyncRoverGroups(log, clusterInstall) },
		func() error { return prowplugin.UpdateProwPluginConfig(log, clusterInstall) },
		func() error { return clustermgmtonboard.NewDexStep(log, kubeClient, clusterInstall).Run(ctx) },
		func() error {
			return clustermgmtonboard.NewQuayioPullThroughCacheStep(log, clusterInstall, kubeClient).Run(ctx)
		},
		func() error { return clustermgmtonboard.NewCertificateStep(log, clusterInstall, kubeClient).Run(ctx) },
	}
	if !update {
		steps = append(steps, func() error { return buildclusters.UpdateBuildClusters(log, clusterInstall) })
	}

	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("run config step: %w", err)
		}
	}
	return nil
}
