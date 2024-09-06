package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type applyConfigStep struct {
	log               *logrus.Entry
	getClusterInstall clustermgmt.ClusterInstallGetter
	cmdBuilder        clustermgmt.CmdBuilder
	cmdRunner         clustermgmt.CmdRunner
}

func (s *applyConfigStep) Name() string {
	return "apply-config"
}

func (s *applyConfigStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "apply-config")

	ci, err := s.getClusterInstall()
	if err != nil {
		return fmt.Errorf("get cluster install: %w", err)
	}

	configDir := fmt.Sprintf("--config-dir=%s/clusters/build-clusters/%s", ci.Onboard.ReleaseRepo, ci.ClusterName)

	kubeconfig := fmt.Sprintf("--kubeconfig=%s", AdminKubeconfig(ci.InstallBase))

	cmd := s.cmdBuilder(ctx, "applyconfig", configDir, "--as=", kubeconfig, "--confirm=true")

	log.Info("Applying configurations")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("applyconfig: %w", err)
	}

	return nil
}

func NewApplyConfigStep(log *logrus.Entry, getClusterInstall clustermgmt.ClusterInstallGetter,
	cmdBuilder clustermgmt.CmdBuilder, cmdRunner clustermgmt.CmdRunner) *applyConfigStep {
	return &applyConfigStep{
		log:               log,
		getClusterInstall: getClusterInstall,
		cmdBuilder:        cmdBuilder,
		cmdRunner:         cmdRunner,
	}
}
