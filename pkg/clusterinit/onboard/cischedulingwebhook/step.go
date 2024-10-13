package cischedulingwebhook

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/sirupsen/logrus"
)

type Provider interface {
	GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, config *clusterinstall.CISchedulingWebhook) (map[string][]interface{}, error)
}

type step struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	provider       Provider
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
}

func (s *step) Name() string {
	return "ci-scheduling-webhook"
}

func (s *step) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.CISchedulingWebhook.Skip {
		log.WithField("reason", s.clusterInstall.Onboard.CISchedulingWebhook.Reason).Info("skipping")
		return nil
	}

	manifestsPath := onboard.CISchedulingWebhookManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := os.MkdirAll(manifestsPath, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", manifestsPath, err)
	}

	manifests, err := s.provider.GenerateManifests(ctx, log, s.clusterInstall, &s.clusterInstall.Onboard.CISchedulingWebhook)
	if err != nil {
		return fmt.Errorf("generate manifests: %w", err)
	}

	for name, manifest := range manifests {
		manifestBytes, err := cinitmanifest.Marshal(manifest, s.clusterInstall.Onboard.CISchedulingWebhook.Patches)
		if err != nil {
			return fmt.Errorf("marshal manifests: %w", err)
		}
		manifestPath := path.Join(manifestsPath, name)
		if err := s.writeManifest(manifestPath, manifestBytes, 0644); err != nil {
			return fmt.Errorf("write manifest %s: %w", manifestPath, err)
		}
	}

	if s.clusterInstall.Onboard.CISchedulingWebhook.GenerateDNS {
		log.Info("Writing dns manifest")
		dnsBytes, err := yaml.Marshal(s.dnsManifest())
		if err != nil {
			return fmt.Errorf("marshal dns: %w", err)
		}
		dnsPath := onboard.CISchedulingWebhookDNSPath(manifestsPath)
		if err := s.writeManifest(dnsPath, dnsBytes, 0644); err != nil {
			return fmt.Errorf("write dns manifest %s: %w", dnsPath, err)
		}
	}

	if err := s.commonSymlink(); err != nil {
		return err
	}

	log.WithField("path", manifestsPath).Info("machines generated")
	return nil
}

func (s *step) commonSymlink() error {
	linkName := onboard.CISchedulingWebhookManifestsCommonPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	target := onboard.CISchedulingWebhookCommonPath(s.clusterInstall.Onboard.ReleaseRepo)
	if err := os.Symlink(target, linkName); err != nil && !os.IsExist(err) {
		return fmt.Errorf("symlink %s -> %s", linkName, target)
	}
	return nil
}

func (s *step) dnsManifest() map[string]interface{} {
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

func NewStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall, provider Provider) *step {
	return &step{
		log:            log,
		clusterInstall: clusterInstall,
		provider:       provider,
		writeManifest:  os.WriteFile,
	}
}
