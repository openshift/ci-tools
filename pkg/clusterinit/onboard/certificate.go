package onboard

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/library-go/pkg/image/reference"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type certificateGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
	kubeClient     ctrlruntimeclient.Client
}

func (s *certificateGenerator) Name() string {
	return "certificate"
}

func (s *certificateGenerator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.Certificate.SkipStep
}

func (s *certificateGenerator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.Certificate.ExcludeManifest
}

func (s *certificateGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.Certificate.Patches
}

func (s *certificateGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	host, err := s.imageRegistryPublicHost(ctx, log)
	if err != nil {
		return nil, fmt.Errorf("image registry public host: %w", err)
	}

	manifests := s.generateCertificateManifests(s.clusterInstall.InstallConfig.BaseDomain, host)

	outputPath := CertificateManifestPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]interface{})
	pathToManifests[outputPath] = manifests

	return pathToManifests, nil
}

func (s *certificateGenerator) imageRegistryPublicHost(ctx context.Context, log *logrus.Entry) (string, error) {
	if s.clusterInstall.Onboard.Certificate.ImageRegistryPublicHost != "" {
		log.Info("override image registry public host from config")
		return s.clusterInstall.Onboard.Certificate.ImageRegistryPublicHost, nil
	}

	isList := imagev1.ImageStreamList{}
	if err := s.kubeClient.List(ctx, &isList, &ctrlruntimeclient.ListOptions{Namespace: "openshift"}); err != nil {
		return "", fmt.Errorf("image streams: %w", err)
	}

	for i := range isList.Items {
		is := &isList.Items[i]
		if value := is.Status.PublicDockerImageRepository; len(value) > 0 {
			ref, err := reference.Parse(value)
			if err != nil {
				return "", fmt.Errorf("parse docker image repository: %w", err)
			}
			return ref.Registry, nil
		}
	}
	return "", fmt.Errorf("no public registry host could be located")
}

func (s *certificateGenerator) generateCertificateManifests(baseDomain, imageRegistryHost string) []interface{} {
	manifests := make([]interface{}, 0)
	platform := "aws"
	project := "openshift-ci-infra"
	issuer := "cert-issuer-aws"
	if strings.Contains(baseDomain, "gcp") {
		platform = "gcp"
		project = "openshift-ci-build-farm"
		issuer = "cert-issuer-ci-build-farm"
	}

	apiServerCert := map[string]interface{}{
		"kind": "Certificate",
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"project":  project,
				"platform": platform,
			},
			"name":      "apiserver-tls",
			"namespace": "openshift-config",
		},
		"spec": map[string]interface{}{
			"dnsNames": []interface{}{
				fmt.Sprintf("api.%s.%s", s.clusterInstall.ClusterName, baseDomain),
			},
			"issuerRef": map[string]interface{}{
				"kind": "ClusterIssuer",
				"name": issuer,
			},
			"secretName": "apiserver-tls",
		},
		"apiVersion": "cert-manager.io/v1",
	}

	appsCert := map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"project":  project,
				"platform": platform,
			},
			"name":      "apps-tls",
			"namespace": "openshift-ingress",
		},
		"spec": map[string]interface{}{
			"dnsNames": []interface{}{
				fmt.Sprintf("*.apps.%s.%s", s.clusterInstall.ClusterName, baseDomain),
			},
			"issuerRef": map[string]interface{}{
				"kind": "ClusterIssuer",
				"name": issuer,
			},
			"secretName": "apps-tls",
		},
	}

	imageRegistryCert := map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"gcp-project": "openshift-ci-infra",
			},
			"name":      "registry-tls",
			"namespace": "openshift-image-registry",
		},
		"spec": map[string]interface{}{
			"dnsNames": []interface{}{
				imageRegistryHost,
			},
			"issuerRef": map[string]interface{}{
				"kind": "ClusterIssuer",
				"name": "cert-issuer",
			},
			"secretName": "public-route-tls",
		},
	}

	if !(*s.clusterInstall.Onboard.OSD || *s.clusterInstall.Onboard.Hosted || *s.clusterInstall.Onboard.Unmanaged) {
		manifests = append(manifests, apiServerCert, appsCert)
	}
	manifests = append(manifests, imageRegistryCert)

	return manifests
}

func NewCertificateGenerator(clusterInstall *clusterinstall.ClusterInstall, kubeClient ctrlruntimeclient.Client) *certificateGenerator {
	return &certificateGenerator{
		clusterInstall: clusterInstall,
		kubeClient:     kubeClient,
	}
}
