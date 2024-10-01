package onboard

import (
	"context"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

type sanitizeProwjobStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *sanitizeProwjobStep) Name() string { return "sanitize-prowjob" }

func (s *sanitizeProwjobStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "sanitize-prowjob")
	s.log.Info("Updating sanitize-prow-jobs config")
	filename := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "core-services", "sanitize-prow-jobs", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c dispatcher.Config
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	s.updateSanitizeProwJobsConfig(&c)
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func (s *sanitizeProwjobStep) updateSanitizeProwJobsConfig(c *dispatcher.Config) {
	clusterName := s.clusterInstall.ClusterName
	appGroup := c.Groups[api.ClusterAPPCI]
	metadata := RepoMetadata()
	appGroup.Jobs = sets.List(sets.New[string](appGroup.Jobs...).
		Insert(metadata.JobName(jobconfig.PresubmitPrefix, clusterName+"-dry")).
		Insert(metadata.JobName(jobconfig.PostsubmitPrefix, clusterName+"-apply")).
		Insert(metadata.SimpleJobName(jobconfig.PeriodicPrefix, clusterName+"-apply")))
	c.Groups[api.ClusterAPPCI] = appGroup
}

func NewSanitizeProwjobStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *sanitizeProwjobStep {
	return &sanitizeProwjobStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
