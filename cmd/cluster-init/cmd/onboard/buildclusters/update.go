package buildclusters

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

type Options struct {
	ClusterName string
	ReleaseRepo string
	Unmanaged   bool
	OSD         bool
	Hosted      bool
}

type BuildClusters struct {
	Managed []string `json:"managed,omitempty"`
	Hosted  []string `json:"hosted,omitempty"`
	Osd     []string `json:"osd,omitempty"`
}

func UpdateBuildClusters(log *logrus.Entry, o Options) error {
	log = log.WithField("step", "update-build-clusters")
	if o.Unmanaged {
		log.Infof("skipping build clusters config update for unmanaged cluster: %s", o.ClusterName)
		return nil
	}
	log.Infof("updating build clusters config to add: %s", o.ClusterName)
	buildClusters, err := LoadBuildClusters(o)
	if err != nil {
		return err
	}

	buildClusters.Managed = append(buildClusters.Managed, o.ClusterName)
	if o.Hosted {
		buildClusters.Hosted = append(buildClusters.Hosted, o.ClusterName)
	}

	if o.OSD {
		buildClusters.Osd = append(buildClusters.Osd, o.ClusterName)
	}

	rawYaml, err := yaml.Marshal(buildClusters)
	if err != nil {
		return err
	}
	return os.WriteFile(buildClustersFile(o), rawYaml, 0644)
}

func LoadBuildClusters(o Options) (*BuildClusters, error) {
	filename := buildClustersFile(o)
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

func buildClustersFile(o Options) string {
	return filepath.Join(o.ReleaseRepo, "clusters", "build-clusters", "_cluster-init.yaml")
}
