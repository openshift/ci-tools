package onboard

import (
	"context"
	"os"
	"slices"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

type buildClusterStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

type buildClusters struct {
	Managed []string `json:"managed,omitempty"`
	Hosted  []string `json:"hosted,omitempty"`
	Osd     []string `json:"osd,omitempty"`
}

func (s *buildClusterStep) Name() string { return "build-cluster" }

func (s *buildClusterStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "update-build-clusters")
	if *s.clusterInstall.Onboard.Unmanaged {
		s.log.Infof("skipping build clusters config update for unmanaged cluster: %s", s.clusterInstall.ClusterName)
		return nil
	}
	s.log.Infof("updating build clusters config to add: %s", s.clusterInstall.ClusterName)
	buildClusters, err := s.Load()
	if err != nil {
		return err
	}

	if !slices.Contains(buildClusters.Managed, s.clusterInstall.ClusterName) {
		buildClusters.Managed = append(buildClusters.Managed, s.clusterInstall.ClusterName)
	}

	if *s.clusterInstall.Onboard.Hosted {
		if !slices.Contains(buildClusters.Hosted, s.clusterInstall.ClusterName) {
			buildClusters.Hosted = append(buildClusters.Hosted, s.clusterInstall.ClusterName)
		}
	}

	if *s.clusterInstall.Onboard.OSD {
		if !slices.Contains(buildClusters.Osd, s.clusterInstall.ClusterName) {
			buildClusters.Osd = append(buildClusters.Osd, s.clusterInstall.ClusterName)
		}
	}

	rawYaml, err := yaml.Marshal(buildClusters)
	if err != nil {
		return err
	}
	return os.WriteFile(BuildClustersPath(s.clusterInstall.Onboard.ReleaseRepo), rawYaml, 0644)
}

func (s *buildClusterStep) Load() (*buildClusters, error) {
	filename := BuildClustersPath(s.clusterInstall.Onboard.ReleaseRepo)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var buildClusters buildClusters
	if err = yaml.Unmarshal(data, &buildClusters); err != nil {
		return nil, err
	}
	return &buildClusters, nil
}

func NewBuildClusterStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *buildClusterStep {
	return &buildClusterStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
