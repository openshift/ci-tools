package sanitizeprowjob

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

func UpdateSanitizeProwJobs(log *logrus.Entry, ci *clusterinstall.ClusterInstall) error {
	log = log.WithField("step", "sanitize-prowjob")
	log.Info("Updating sanitize-prow-jobs config")
	filename := filepath.Join(ci.Onboard.ReleaseRepo, "core-services", "sanitize-prow-jobs", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c dispatcher.Config
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	updateSanitizeProwJobsConfig(&c, ci.ClusterName)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func updateSanitizeProwJobsConfig(c *dispatcher.Config, clusterName string) {
	appGroup := c.Groups[api.ClusterAPPCI]
	metadata := onboard.RepoMetadata()
	appGroup.Jobs = sets.List(sets.New[string](appGroup.Jobs...).
		Insert(metadata.JobName(jobconfig.PresubmitPrefix, clusterName+"-dry")).
		Insert(metadata.JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply")).
		Insert(metadata.SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply")))
	c.Groups[api.ClusterAPPCI] = appGroup
}
