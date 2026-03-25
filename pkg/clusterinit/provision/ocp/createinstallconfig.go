package ocp

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	installerTypes "github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type createInstallConfigStep struct {
	log                  *logrus.Entry
	clusterInstall       *clusterinstall.ClusterInstall
	cmdBuilder           types.CmdBuilder
	cmdRunner            types.CmdRunner
	installConfigPatcher types.InstallConfigPatcher
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

	installConfigPath := fmt.Sprintf("%s/install-config.yaml", path.Join(s.clusterInstall.InstallBase, "ocp-install-base"))
	if err := s.installConfigPatcher(installConfigPath); err != nil {
		return fmt.Errorf("patch install-config: %w", err)
	}

	return nil
}

func NewCreateInstallConfigStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall,
	cmdBuilder types.CmdBuilder, cmdRunner types.CmdRunner, installConfigPatcher types.InstallConfigPatcher) *createInstallConfigStep {
	return &createInstallConfigStep{
		log:                  log,
		clusterInstall:       clusterInstall,
		cmdBuilder:           cmdBuilder,
		cmdRunner:            cmdRunner,
		installConfigPatcher: installConfigPatcher,
	}
}

func PatchInstallConfig(installConfigPath string) error {
	data, err := os.ReadFile(installConfigPath)
	if err != nil {
		return fmt.Errorf("patching install-config: %w", err)
	}

	var installConfig installerTypes.InstallConfig
	err = yaml.Unmarshal(data, &installConfig)
	if err != nil {
		return fmt.Errorf("unmarshalling install-config: %w", err)
	}

	if installConfig.Platform.AWS != nil {
		throughput := int32(250)
		installConfig.ControlPlane.Platform.AWS.EC2RootVolume.Throughput = &throughput
	}

	marshalledInstallConfig, err := yaml.Marshal(installConfig)
	if err != nil {
		return fmt.Errorf("marshalling install-config: %w", err)
	}
	return os.WriteFile(installConfigPath, marshalledInstallConfig, 0644)
}
