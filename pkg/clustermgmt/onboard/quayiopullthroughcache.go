package onboard

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type quayioPullThroughCacheStep struct {
	log            *logrus.Entry
	clusterInstall *clustermgmt.ClusterInstall
	kubeClient     KubeClientGetter
	writeTemplate  func(name string, data []byte, perm fs.FileMode) error
}

func (s *quayioPullThroughCacheStep) Name() string {
	return "quayio-pull-through-cache"
}

func (s *quayioPullThroughCacheStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	mirrorURI, err := s.mirrorURI(ctx)
	if err != nil {
		return fmt.Errorf("mirror uri: %w", err)
	}

	manifest := generatePullThroughCacheManifest(mirrorURI)
	manifestMarshaled, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	outputPath := QuayioPullThroughCacheManifestPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.writeTemplate(outputPath, manifestMarshaled, 0644); err != nil {
		return fmt.Errorf("write template %s: %w", outputPath, err)
	}

	log.WithField("template", outputPath).Info("quay.io pull through cache generated")
	return nil
}

func (s *quayioPullThroughCacheStep) mirrorURI(ctx context.Context) (string, error) {
	mu, found := s.clusterInstall.Onboard.QuayioPullThroughCache.MirrorURIs[s.clusterInstall.ClusterName]
	if found {
		return mu, nil
	}

	client, err := s.kubeClient()
	if err != nil {
		return "", fmt.Errorf("get kube client: %w", err)
	}

	imageRegConfig := imageregistryv1.Config{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: "openshift-image-registry", Name: "cluster"}, &imageRegConfig); err != nil {
		return "", fmt.Errorf("get cluster config: %w", err)
	}

	if imageRegConfig.Spec.Storage.GCS != nil {
		return "quayio-pull-through-cache-gcs-ci.apps.ci.l2s4.p1.openshiftapps.com", nil
	}
	return "quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com", nil
}

func generatePullThroughCacheManifest(mirror string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "operator.openshift.io/v1alpha1",
		"kind":       "ImageContentSourcePolicy",
		"metadata": map[string]interface{}{
			"name": "quayio-pull-through-cache-icsp",
		},
		"spec": map[string]interface{}{
			"repositoryDigestMirrors": []interface{}{
				map[string]interface{}{
					"mirrors": []interface{}{
						mirror,
					},
					"source": "quay.io",
				},
			},
		},
	}
}

func NewQuayioPullThroughCacheStep(log *logrus.Entry, clusterInstall *clustermgmt.ClusterInstall,
	kubeClient KubeClientGetter) *quayioPullThroughCacheStep {
	return &quayioPullThroughCacheStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeTemplate:  os.WriteFile,
		kubeClient:     kubeClient,
	}
}
