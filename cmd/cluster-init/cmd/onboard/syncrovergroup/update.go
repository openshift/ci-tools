package syncrovergroup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/openshift/ci-tools/pkg/group"
)

func UpdateSyncRoverGroups(_ *logrus.Entry, ci *clusterinstall.ClusterInstall) error {
	filename := filepath.Join(ci.Onboard.ReleaseRepo, "core-services", "sync-rover-groups", "_config.yaml")
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
	c.ClusterGroups["build-farm"] = sets.List(sets.New[string](c.ClusterGroups["build-farm"]...).Insert(ci.ClusterName))
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}
