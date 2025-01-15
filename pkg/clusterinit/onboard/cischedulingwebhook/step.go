package cischedulingwebhook

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type Provider interface {
	GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, config *clusterinstall.CISchedulingWebhook) (map[string][]interface{}, error)
}

type generator struct {
	clusterInstall *clusterinstall.ClusterInstall
	provider       Provider
}

func (s *generator) Name() string {
	return "ci-scheduling-webhook"
}

func (s *generator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.CISchedulingWebhook.SkipStep
}

func (s *generator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.CISchedulingWebhook.ExcludeManifest
}

func (s *generator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.CISchedulingWebhook.Patches
}

func (s *generator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	manifestsPath := onboard.CISchedulingWebhookManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]interface{})

	manifests, err := s.provider.GenerateManifests(ctx, log, s.clusterInstall, &s.clusterInstall.Onboard.CISchedulingWebhook)
	if err != nil {
		return nil, fmt.Errorf("generate manifests: %w", err)
	}

	for name, manifest := range manifests {
		pathToManifests[path.Join(manifestsPath, name)] = manifest
	}

	if s.clusterInstall.Onboard.CISchedulingWebhook.GenerateDNS {
		log.Info("Writing dns manifest")
		dnsPath := onboard.CISchedulingWebhookDNSPath(manifestsPath)
		pathToManifests[dnsPath] = []interface{}{s.dnsManifest()}
	}

	if err := s.commonSymlink(); err != nil {
		return nil, err
	}

	return pathToManifests, nil
}

func (s *generator) commonSymlink() error {
	linkName := onboard.CISchedulingWebhookManifestsCommonPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	target := onboard.CISchedulingWebhookCommonRelativePath

	parent := path.Dir(linkName)
	if err := os.MkdirAll(parent, 0744); err != nil {
		return fmt.Errorf("mkdir %s: %w", parent, err)
	}

	if err := os.Symlink(target, linkName); err != nil && !os.IsExist(err) {
		return fmt.Errorf("symlink %s -> %s: %w", linkName, target, err)
	}

	return nil
}

func (s *generator) dnsManifest() map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "default",
		},
		"spec": map[string]interface{}{
			"logLevel": "Normal",
			"nodePlacement": map[string]interface{}{
				"tolerations": []interface{}{
					map[string]interface{}{
						"operator": "Exists",
					},
				},
			},
			"operatorLogLevel": "Normal",
			"upstreamResolvers": map[string]interface{}{
				"policy": "Sequential",
				"upstreams": []interface{}{
					map[string]interface{}{
						"port": 53,
						"type": "SystemResolvConf",
					},
				},
			},
		},
		"apiVersion": "operator.openshift.io/v1",
		"kind":       "DNS",
	}
}

func NewGenerator(clusterInstall *clusterinstall.ClusterInstall, provider Provider) *generator {
	return &generator{
		clusterInstall: clusterInstall,
		provider:       provider,
	}
}
