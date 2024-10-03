package ocp

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type createClusterStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	cmdBuilder     clusterinit.CmdBuilder
	cmdRunner      clusterinit.CmdRunner
}

func (s *createClusterStep) Name() string {
	return "create-ocp-cluster"
}

func (s *createClusterStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: ocp: cluster")

	cmd := s.cmdBuilder(ctx, "openshift-install", "create", "cluster", "--log-level=debug",
		fmt.Sprintf("--dir=%s", path.Join(s.clusterInstall.InstallBase, "ocp-install-base")))

	log.Info("Creating cluster")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("create cluster: %w", err)
	}

	return nil
}

func NewCreateClusterStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall,
	cmdBuilder clusterinit.CmdBuilder, cmdRunner clusterinit.CmdRunner) *createClusterStep {
	return &createClusterStep{
		log:            log,
		clusterInstall: clusterInstall,
		cmdBuilder:     cmdBuilder,
		cmdRunner:      cmdRunner,
	}
}
