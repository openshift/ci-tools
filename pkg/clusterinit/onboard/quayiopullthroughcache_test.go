package onboard

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestUpdateQuayioPullThroughCache(t *testing.T) {
	releaseRepo := "/release/repo"
	clusterName := "build99"
	for _, tc := range []struct {
		name           string
		clusterInstall clusterinstall.ClusterInstall
		config         imageregistryv1.Config
		wantManifests  map[string][]interface{}
		wantErr        error
	}{
		{
			name: "Non GCS cache",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: releaseRepo,
					OSD:         ptr.To(false),
					Hosted:      ptr.To(false),
					Unmanaged:   ptr.To(false),
				},
			},
			config: imageregistryv1.Config{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "openshift-image-registry",
					Name:      "cluster"},
				Spec: imageregistryv1.ImageRegistrySpec{
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						S3: &imageregistryv1.ImageRegistryConfigStorageS3{}}}},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/assets/quayio-pull-through-cache-icsp.yaml": {
					map[string]interface{}{
						"apiVersion": "operator.openshift.io/v1alpha1",
						"kind":       "ImageContentSourcePolicy",
						"metadata": map[string]interface{}{
							"name": "quayio-pull-through-cache-icsp",
						},
						"spec": map[string]interface{}{
							"repositoryDigestMirrors": []interface{}{
								map[string]interface{}{
									"mirrors": []interface{}{
										"quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com",
									},
									"source": "quay.io",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "GCS cache",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: releaseRepo,
					OSD:         ptr.To(false),
					Hosted:      ptr.To(false),
					Unmanaged:   ptr.To(false),
				},
			},
			config: imageregistryv1.Config{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "openshift-image-registry",
					Name:      "cluster"},
				Spec: imageregistryv1.ImageRegistrySpec{
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						GCS: &imageregistryv1.ImageRegistryConfigStorageGCS{}}}},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/assets/quayio-pull-through-cache-icsp.yaml": {
					map[string]interface{}{
						"apiVersion": "operator.openshift.io/v1alpha1",
						"kind":       "ImageContentSourcePolicy",
						"metadata": map[string]interface{}{
							"name": "quayio-pull-through-cache-icsp",
						},
						"spec": map[string]interface{}{
							"repositoryDigestMirrors": []interface{}{
								map[string]interface{}{
									"mirrors": []interface{}{
										"quayio-pull-through-cache-gcs-ci.apps.ci.l2s4.p1.openshiftapps.com",
									},
									"source": "quay.io",
								},
							},
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := imageregistryv1.AddToScheme(scheme); err != nil {
				t.Fatal("add routev1 to scheme")
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&tc.config).Build()
			generator := NewQuayioPullThroughCacheStep(&tc.clusterInstall, kubeClient)

			manifests, err := generator.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
