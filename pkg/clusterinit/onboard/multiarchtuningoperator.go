package onboard

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type multiarchTuningOperatorStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *multiarchTuningOperatorStep) Name() string {
	return "multiarch-tuning-operator"
}

func (m *multiarchTuningOperatorStep) Run(ctx context.Context) error {
	log := m.log.WithField("step", m.Name())

	if m.clusterInstall.Onboard.MultiarchTuningOperator.Skip {
		log.Info("step is not enabled, skipping")
		return nil
	}

	linkName := MultiarchTuningOperatorPath(m.clusterInstall.Onboard.ReleaseRepo, m.clusterInstall.ClusterName)
	target := "../multiarch_tuning_operator"
	if err := os.Symlink(target, linkName); err != nil && !os.IsExist(err) {
		return fmt.Errorf("symlink %s -> %s", linkName, target)
	}

	return nil
}

func NewMultiarchTuningOperatorStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *multiarchTuningOperatorStep {
	return &multiarchTuningOperatorStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
