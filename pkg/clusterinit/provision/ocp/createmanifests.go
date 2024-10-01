package ocp

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type createManifestsStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	cmdBuilder     clusterinit.CmdBuilder
	cmdRunner      clusterinit.CmdRunner
}

func (s *createManifestsStep) Name() string {
	return "create-ocp-manifests"
}

func (s *createManifestsStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: ocp: manifests")

	cmd := s.cmdBuilder(ctx, "openshift-install", "create", "manifests", "--log-level=debug",
		fmt.Sprintf("--dir=%s", path.Join(s.clusterInstall.InstallBase, "ocp-install-base")))

	log.Info("Creating manifests")
	if err := s.cmdRunner(cmd); err != nil {
		return fmt.Errorf("create manifests: %w", err)
	}

	return nil
}

func NewCreateManifestsStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall,
	cmdBuilder clusterinit.CmdBuilder, cmdRunner clusterinit.CmdRunner) *createManifestsStep {
	return &createManifestsStep{
		log:            log,
		clusterInstall: clusterInstall,
		cmdBuilder:     cmdBuilder,
		cmdRunner:      cmdRunner,
	}
}
