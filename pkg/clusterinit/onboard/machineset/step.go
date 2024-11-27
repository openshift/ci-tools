package machineset

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
)

type Provider interface {
	GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall) (map[string][]interface{}, error)
}

type step struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	provider       Provider
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
}

func (s *step) Name() string {
	return "machineset"
}

func (s *step) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.MachineSet.Skip {
		log.WithField("reason", s.clusterInstall.Onboard.MachineSet.Reason).Info("skipping")
		return nil
	}

	manifestsPath := onboard.MachineSetManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := os.MkdirAll(manifestsPath, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", manifestsPath, err)
	}

	manifests, err := s.provider.GenerateManifests(ctx, log, s.clusterInstall)
	if err != nil {
		return fmt.Errorf("generate manifests: %w", err)
	}

	for name, manifest := range manifests {
		manifestBytes, err := cinitmanifest.Marshal(manifest, s.clusterInstall.Onboard.MachineSet.Patches)
		if err != nil {
			return fmt.Errorf("marshal manifests: %w", err)
		}
		manifestPath := path.Join(manifestsPath, name)
		if err := s.writeManifest(manifestPath, manifestBytes, 0644); err != nil {
			return fmt.Errorf("write manifest %s: %w", manifestPath, err)
		}
	}

	manifestBytes, err := cinitmanifest.Marshal([]interface{}{s.clusterAutoscalerManifest()}, s.clusterInstall.Onboard.MachineSet.Patches)
	if err != nil {
		return fmt.Errorf("marshal clusterautoscaler manifests: %w", err)
	}
	manifestPath := path.Join(manifestsPath, "clusterautoscaler.yaml")
	if err := s.writeManifest(manifestPath, manifestBytes, 0644); err != nil {
		return fmt.Errorf("write clusterautoscaler manifest %s: %w", manifestPath, err)
	}

	log.WithField("path", manifestsPath).Info("machineset generated")
	return nil
}

func (s *step) clusterAutoscalerManifest() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "autoscaling.openshift.io/v1",
		"kind":       "ClusterAutoscaler",
		"metadata": map[string]interface{}{
			"name": "default",
		},
		"spec": map[string]interface{}{
			"maxNodeProvisionTime": "30m",
			"podPriorityThreshold": -10,
		},
	}
}

func NewStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall, provider Provider) *step {
	return &step{
		log:            log,
		clusterInstall: clusterInstall,
		provider:       provider,
		writeManifest:  os.WriteFile,
	}
}
