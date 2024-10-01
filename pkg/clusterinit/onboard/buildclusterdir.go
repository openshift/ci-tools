package onboard

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type buildClusterDirStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *buildClusterDirStep) Name() string { return "build-cluster-dir" }

func (s *buildClusterDirStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "build-cluster-dir")
	clusterDir, err := s.createClusterDir()
	if err != nil {
		return err
	}
	if err := s.createSymlinks(clusterDir); err != nil {
		return err
	}
	return s.createRequiredDirs(clusterDir)
}

func (s *buildClusterDirStep) createClusterDir() (string, error) {
	clusterDir := BuildFarmDirFor(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	_, err := os.Stat(clusterDir)
	if os.IsNotExist(err) {
		s.log.WithField("dir", clusterDir).Info("Creating cluster directory")
		if err := os.MkdirAll(clusterDir, 0777); err != nil {
			return "", fmt.Errorf("failed to create base directory for cluster: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("stat %s: %w", clusterDir, err)
	}
	return clusterDir, nil
}

func (s *buildClusterDirStep) createSymlinks(clusterDir string) error {
	config_dirs := []string{
		"common",
		"common_except_app.ci",
	}

	if !*s.clusterInstall.Onboard.Hosted {
		config_dirs = append(config_dirs, "common_except_hosted")
	}

	for _, item := range config_dirs {
		target := fmt.Sprintf("../%s", item)
		linkName := filepath.Join(clusterDir, item)
		_, err := os.Lstat(linkName)
		if os.IsNotExist(err) {
			s.log.WithFields(map[string]interface{}{"linkName": linkName, "target": target}).Info("Creating symlink")
			if err := os.Symlink(target, linkName); err != nil {
				return fmt.Errorf("failed to symlink %s to %s", linkName, target)
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", linkName, err)
		}
	}
	return nil
}

func (s *buildClusterDirStep) createRequiredDirs(clusterDir string) error {
	for _, name := range []string{"assets", "cert-manager"} {
		dir := path.Join(clusterDir, name)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			s.log.WithField("dir", dir).Info("Creating directory")
			if err := os.Mkdir(dir, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", dir, err)
		}
	}
	return nil
}

func NewBuildClusterDirStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *buildClusterDirStep {
	return &buildClusterDirStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
