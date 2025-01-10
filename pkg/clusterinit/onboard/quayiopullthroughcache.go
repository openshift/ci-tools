package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type quayioPullThroughCacheStep struct {
	clusterInstall *clusterinstall.ClusterInstall
	kubeClient     ctrlruntimeclient.Client
}

func (s *quayioPullThroughCacheStep) Name() string {
	return "quayio-pull-through-cache"
}

func (s *quayioPullThroughCacheStep) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.QuayioPullThroughCache.SkipStep
}

func (s *quayioPullThroughCacheStep) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.QuayioPullThroughCache.ExcludeManifest
}

func (s *quayioPullThroughCacheStep) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.QuayioPullThroughCache.Patches
}

func (s *quayioPullThroughCacheStep) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	mirrorURI, err := s.mirrorURI(ctx)
	if err != nil {
		return nil, fmt.Errorf("mirror uri: %w", err)
	}

	manifest := generatePullThroughCacheManifest(mirrorURI)
	outputPath := QuayioPullThroughCacheManifestPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)

	log.WithField("template", outputPath).Info("quay.io pull through cache generated")
	return map[string][]interface{}{outputPath: {manifest}}, nil
}

func (s *quayioPullThroughCacheStep) mirrorURI(ctx context.Context) (string, error) {
	imageRegConfig := imageregistryv1.Config{}
	if err := s.kubeClient.Get(ctx, types.NamespacedName{Namespace: "openshift-image-registry", Name: "cluster"}, &imageRegConfig); err != nil {
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

func NewQuayioPullThroughCacheStep(clusterInstall *clusterinstall.ClusterInstall, kubeClient ctrlruntimeclient.Client) *quayioPullThroughCacheStep {
	return &quayioPullThroughCacheStep{
		clusterInstall: clusterInstall,
		kubeClient:     kubeClient,
	}
}
