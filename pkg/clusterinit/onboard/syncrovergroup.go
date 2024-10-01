package onboard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/group"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

type syncRoverGroupStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *syncRoverGroupStep) Name() string { return "sync-rover-group" }

func (s *syncRoverGroupStep) Run(ctx context.Context) error {
	filename := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "core-services", "sync-rover-groups", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c group.Config
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.ClusterGroups == nil {
		return fmt.Errorf("`cluster_groups` is not defined in the sync-rover-groups' configuration")
	}
	c.ClusterGroups["build-farm"] = sets.List(sets.New[string](c.ClusterGroups["build-farm"]...).Insert(s.clusterInstall.ClusterName))
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func NewSyncRoverGroupStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *syncRoverGroupStep {
	return &syncRoverGroupStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
