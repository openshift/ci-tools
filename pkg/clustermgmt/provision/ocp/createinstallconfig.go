package ocp

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type createInstallConfigStep struct {
	log               *logrus.Entry
	getClusterInstall clustermgmt.ClusterInstallGetter
	cmdBuilder        clustermgmt.CmdBuilder
	cmdRunner         clustermgmt.CmdRunner
}

func (s *createInstallConfigStep) Name() string {
	return "create-ocp-install-config"
}

func (s *createInstallConfigStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: ocp: install-config")

	ci, err := s.getClusterInstall()
	if err != nil {
		return fmt.Errorf("get cluster install: %w", err)
	}

	cmd := s.cmdBuilder(ctx, "openshift-install", "create", "install-config", "--log-level=debug",
		fmt.Sprintf("--dir=%s", path.Join(ci.InstallBase, "ocp-install-base")))

	log.Info("Creating install-config")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("create install-config: %w", err)
	}

	return nil
}

func NewCreateInstallConfigStep(log *logrus.Entry, getClusterInstall clustermgmt.ClusterInstallGetter,
	cmdBuilder clustermgmt.CmdBuilder, cmdRunner clustermgmt.CmdRunner) *createInstallConfigStep {
	return &createInstallConfigStep{
		log:               log,
		getClusterInstall: getClusterInstall,
		cmdBuilder:        cmdBuilder,
		cmdRunner:         cmdRunner,
	}
}
