package ocp

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type createInstallConfigStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	cmdBuilder     types.CmdBuilder
	cmdRunner      types.CmdRunner
}

func (s *createInstallConfigStep) Name() string {
	return "create-ocp-install-config"
}

func (s *createInstallConfigStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: ocp: install-config")

	cmd := s.cmdBuilder(ctx, "openshift-install", "create", "install-config", "--log-level=debug",
		fmt.Sprintf("--dir=%s", path.Join(s.clusterInstall.InstallBase, "ocp-install-base")))

	log.Info("Creating install-config")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("create install-config: %w", err)
	}

	return nil
}

func NewCreateInstallConfigStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall,
	cmdBuilder types.CmdBuilder, cmdRunner types.CmdRunner) *createInstallConfigStep {
	return &createInstallConfigStep{
		log:            log,
		clusterInstall: clusterInstall,
		cmdBuilder:     cmdBuilder,
		cmdRunner:      cmdRunner,
	}
}
