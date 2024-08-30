package buildclusterdir

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
)

type Options struct {
	ReleaseRepo string
	ClusterName string
	Update      bool
}

func UpdateClusterBuildFarmDir(log *logrus.Entry, o Options, hostedClusters []string) error {
	log = log.WithField("step", "build-cluster-dir")
	buildDir := onboard.BuildFarmDirFor(o.ReleaseRepo, o.ClusterName)
	if o.Update {
		log.Infof("Updating build dir: %s", buildDir)
	} else {
		log.Infof("creating build dir: %s", buildDir)
		if err := os.MkdirAll(buildDir, 0777); err != nil {
			return fmt.Errorf("failed to create base directory for cluster: %w", err)
		}
	}

	config_dirs := []string{
		"common",
		"common_except_app.ci",
	}

	hostedClustersSet := sets.New[string](hostedClusters...)
	if !hostedClustersSet.Has(o.ClusterName) {
		config_dirs = append(config_dirs, "common_except_hosted")
	}

	for _, item := range config_dirs {
		target := fmt.Sprintf("../%s", item)
		source := filepath.Join(buildDir, item)
		if o.Update {
			if err := os.RemoveAll(source); err != nil {
				return fmt.Errorf("failed to remove symlink %s, error: %w", source, err)
			}
		}
		if err := os.Symlink(target, source); err != nil {
			return fmt.Errorf("failed to symlink %s to ../%s", item, item)
		}
	}
	return nil
}
