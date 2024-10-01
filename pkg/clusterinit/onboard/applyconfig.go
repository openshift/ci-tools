package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type applyConfigStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	cmdBuilder     clusterinit.CmdBuilder
	cmdRunner      clusterinit.CmdRunner
}

func (s *applyConfigStep) Name() string {
	return "apply-config"
}

func (s *applyConfigStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "apply-config")

	configDir := fmt.Sprintf("--config-dir=%s/clusters/build-clusters/%s", s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	kubeconfig := fmt.Sprintf("--kubeconfig=%s", AdminKubeconfig(s.clusterInstall.InstallBase))
	cmd := s.cmdBuilder(ctx, "applyconfig", configDir, "--as=", kubeconfig, "--confirm=true")

	log.Info("Applying configurations")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("applyconfig: %w", err)
	}

	return nil
}

func NewApplyConfigStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall,
	cmdBuilder clusterinit.CmdBuilder, cmdRunner clusterinit.CmdRunner) *applyConfigStep {
	return &applyConfigStep{
		log:            log,
		clusterInstall: clusterInstall,
		cmdBuilder:     cmdBuilder,
		cmdRunner:      cmdRunner,
	}
}
