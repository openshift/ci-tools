package machineset

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type Provider interface {
	GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall) (map[string][]interface{}, error)
}

type generator struct {
	clusterInstall *clusterinstall.ClusterInstall
	provider       Provider
}

func (s *generator) Name() string {
	return "machineset"
}

func (s *generator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.MachineSet.SkipStep
}

func (s *generator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.MachineSet.ExcludeManifest
}

func (s *generator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.MachineSet.Patches
}

func (s *generator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	manifestsPath := onboard.MachineSetManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]interface{})

	manifests, err := s.provider.GenerateManifests(ctx, log, s.clusterInstall)
	if err != nil {
		return nil, fmt.Errorf("generate manifests: %w", err)
	}

	for name, manifest := range manifests {
		pathToManifests[path.Join(manifestsPath, name)] = manifest
	}
	pathToManifests[path.Join(manifestsPath, "clusterautoscaler.yaml")] = []interface{}{s.clusterAutoscalerManifest()}

	return pathToManifests, nil
}

func (s *generator) clusterAutoscalerManifest() map[string]interface{} {
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

func NewGenerator(clusterInstall *clusterinstall.ClusterInstall, provider Provider) *generator {
	return &generator{
		clusterInstall: clusterInstall,
		provider:       provider,
	}
}
