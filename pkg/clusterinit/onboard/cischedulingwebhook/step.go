package cischedulingwebhook

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	citoolsyaml "github.com/openshift/ci-tools/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/onboard"
	"github.com/sirupsen/logrus"
)

type Provider interface {
	GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, workload, arch string, config *clusterinstall.CISchedulingWebhookWorkload) ([]interface{}, error)
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

	config := &s.clusterInstall.Onboard.CISchedulingWebhook
	for workload, config := range config.Workloads {
		for _, arch := range config.Archs {
			manifests, err := s.provider.GenerateManifests(ctx, log, s.clusterInstall, workload, string(arch), &config)
			if err != nil {
				return fmt.Errorf("generate machineset: %w", err)
			}
			manifestMarshaled, err := citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			manifestPath := path.Join(manifestsPath, fmt.Sprintf("ci-%s-worker-%s.yaml", workload, arch))
			if err := s.writeManifest(manifestPath, manifestMarshaled, 0644); err != nil {
				return fmt.Errorf("write manifest %s: %w", manifestPath, err)
			}
		}
	}

	if s.clusterInstall.Onboard.CISchedulingWebhook.GenerateDNS {
		log.Info("Writing dns manifest")
		dnsBytes, err := yaml.Marshal(dnsManifest())
		if err != nil {
			return fmt.Errorf("marshal dns: %w", err)
		}
		dnsPath := onboard.CISchedulingWebhookDNSPath(manifestsPath)
		if err := s.writeManifest(dnsPath, dnsBytes, 0644); err != nil {
			return fmt.Errorf("write dns manifest %s: %w", dnsPath, err)
		}
	}

	log.WithField("path", manifestsPath).Info("machines generated")
	return nil
}

func dnsManifest() map[string]interface{} {
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
