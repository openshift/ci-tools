package ocp

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type createClusterStep struct {
	log               *logrus.Entry
	getClusterInstall clustermgmt.ClusterInstallGetter
	cmdBuilder        clustermgmt.CmdBuilder
	cmdRunner         clustermgmt.CmdRunner
}

func (s *createClusterStep) Name() string {
	return "create-ocp-cluster"
}

func (s *createClusterStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: ocp: cluster")

	ci, err := s.getClusterInstall()
	if err != nil {
		return fmt.Errorf("get cluster install: %w", err)
	}

	cmd := s.cmdBuilder(ctx, "openshift-install", "create", "cluster", "--log-level=debug",
		fmt.Sprintf("--dir=%s", path.Join(ci.InstallBase, "ocp-install-base")))

	log.Info("Creating cluster")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("create cluster: %w", err)
	}

	return nil
}

func NewCreateClusterStep(log *logrus.Entry, getClusterInstall clustermgmt.ClusterInstallGetter,
	cmdBuilder clustermgmt.CmdBuilder, cmdRunner clustermgmt.CmdRunner) *createClusterStep {
	return &createClusterStep{
		log:               log,
		getClusterInstall: getClusterInstall,
		cmdBuilder:        cmdBuilder,
		cmdRunner:         cmdRunner,
	}
}
