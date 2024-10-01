package buildclusterdir

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
)

func UpdateClusterBuildFarmDir(log *logrus.Entry, ci *clusterinstall.ClusterInstall) error {
	log = log.WithField("step", "build-cluster-dir")
	clusterDir, err := createClusterDir(log, ci)
	if err != nil {
		return err
	}
	if err := createSymlinks(log, clusterDir, ci); err != nil {
		return err
	}
	return createRequiredDirs(log, clusterDir)
}

func createClusterDir(log *logrus.Entry, ci *clusterinstall.ClusterInstall) (string, error) {
	clusterDir := onboard.BuildFarmDirFor(ci.Onboard.ReleaseRepo, ci.ClusterName)
	_, err := os.Stat(clusterDir)
	if os.IsNotExist(err) {
		log.WithField("dir", clusterDir).Info("Creating cluster directory")
		if err := os.MkdirAll(clusterDir, 0777); err != nil {
			return "", fmt.Errorf("failed to create base directory for cluster: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("stat %s: %w", clusterDir, err)
	}
	return clusterDir, nil
}

func createSymlinks(log *logrus.Entry, clusterDir string, ci *clusterinstall.ClusterInstall) error {
	config_dirs := []string{
		"common",
		"common_except_app.ci",
	}

	if !*ci.Onboard.Hosted {
		config_dirs = append(config_dirs, "common_except_hosted")
	}

	for _, item := range config_dirs {
		target := fmt.Sprintf("../%s", item)
		linkName := filepath.Join(clusterDir, item)
		_, err := os.Lstat(linkName)
		if os.IsNotExist(err) {
			log.WithFields(map[string]interface{}{"linkName": linkName, "target": target}).Info("Creating symlink")
			if err := os.Symlink(target, linkName); err != nil {
				return fmt.Errorf("failed to symlink %s to %s", linkName, target)
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", linkName, err)
		}
	}
	return nil
}

func createRequiredDirs(log *logrus.Entry, clusterDir string) error {
	for _, name := range []string{"assets", "cert-manager"} {
		dir := path.Join(clusterDir, name)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			log.WithField("dir", dir).Info("Creating directory")
			if err := os.Mkdir(dir, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", dir, err)
		}
	}
	return nil
}
