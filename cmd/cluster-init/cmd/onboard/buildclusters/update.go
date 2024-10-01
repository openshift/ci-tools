package buildclusters

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
)

type BuildClusters struct {
	Managed []string `json:"managed,omitempty"`
	Hosted  []string `json:"hosted,omitempty"`
	Osd     []string `json:"osd,omitempty"`
}

func UpdateBuildClusters(log *logrus.Entry, ci *clusterinstall.ClusterInstall) error {
	log = log.WithField("step", "update-build-clusters")
	if *ci.Onboard.Unmanaged {
		log.Infof("skipping build clusters config update for unmanaged cluster: %s", ci.ClusterName)
		return nil
	}
	log.Infof("updating build clusters config to add: %s", ci.ClusterName)
	buildClusters, err := LoadBuildClusters(ci.Onboard.ReleaseRepo)
	if err != nil {
		return err
	}

	buildClusters.Managed = append(buildClusters.Managed, ci.ClusterName)
	if *ci.Onboard.Hosted {
		buildClusters.Hosted = append(buildClusters.Hosted, ci.ClusterName)
	}

	if *ci.Onboard.OSD {
		buildClusters.Osd = append(buildClusters.Osd, ci.ClusterName)
	}

	rawYaml, err := yaml.Marshal(buildClusters)
	if err != nil {
		return err
	}
	return os.WriteFile(buildClustersFile(ci.Onboard.ReleaseRepo), rawYaml, 0644)
}

func LoadBuildClusters(releaseRepo string) (*BuildClusters, error) {
	filename := buildClustersFile(releaseRepo)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var buildClusters BuildClusters
	if err = yaml.Unmarshal(data, &buildClusters); err != nil {
		return nil, err
	}
	return &buildClusters, nil
}

func buildClustersFile(releaseRepo string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", "_cluster-init.yaml")
}
