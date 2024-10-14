package onboard

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type commonSymlinkStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *commonSymlinkStep) Name() string {
	return "common-symlink"
}

func (s *commonSymlinkStep) Run(ctx context.Context) error {
	symlink := func(linkName, target string) error {
		if err := os.Symlink(target, linkName); err != nil && !os.IsExist(err) {
			return fmt.Errorf("symlink %s -> %s", linkName, target)
		}
		return nil
	}
	rr := s.clusterInstall.Onboard.ReleaseRepo
	clusterName := s.clusterInstall.ClusterName
	if err := symlink(CommonSymlinkPath(rr, clusterName), "../common_managed"); err != nil {
		return err
	}
	if s.clusterInstall.IsOCP() {
		if err := symlink(CommonSymlinkPath(rr, clusterName), "../common_ocp"); err != nil {
			return err
		}
	}
	return nil
}

func NewCommonSymlinkStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *commonSymlinkStep {
	return &commonSymlinkStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
