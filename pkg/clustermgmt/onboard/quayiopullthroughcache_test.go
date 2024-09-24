package onboard

import (
	"context"
	"io/fs"
	"path"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

func TestUpdateQuayioPullThroughCache(t *testing.T) {
	releaseRepo := "/release/repo"
	clusterName := "build99"
	for _, tc := range []struct {
		name           string
		clusterInstall clustermgmt.ClusterInstall
		config         imageregistryv1.Config
		wantManifest   string
		wantErr        error
	}{
		{
			name: "Non GCS cache",
			clusterInstall: clustermgmt.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clustermgmt.Onboard{
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
			wantManifest: `apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: quayio-pull-through-cache-icsp
spec:
  repositoryDigestMirrors:
  - mirrors:
    - quayio-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com
    source: quay.io
`,
		},
		{
			name: "GCS cache",
			clusterInstall: clustermgmt.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clustermgmt.Onboard{
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
			wantManifest: `apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: quayio-pull-through-cache-icsp
spec:
  repositoryDigestMirrors:
  - mirrors:
    - quayio-pull-through-cache-gcs-ci.apps.ci.l2s4.p1.openshiftapps.com
    source: quay.io
`,
		},
		{
			name: "Override from config",
			clusterInstall: clustermgmt.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clustermgmt.Onboard{
					ReleaseRepo: releaseRepo,
					OSD:         ptr.To(false),
					Hosted:      ptr.To(false),
					Unmanaged:   ptr.To(false),
					QuayioPullThroughCache: clustermgmt.QuayioPullThroughCache{
						MirrorURIs: map[string]string{
							"build99": "fake",
						},
					},
				},
			},
			config: imageregistryv1.Config{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "openshift-image-registry",
					Name:      "cluster"},
				Spec: imageregistryv1.ImageRegistrySpec{
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						GCS: &imageregistryv1.ImageRegistryConfigStorageGCS{}}}},
			wantManifest: `apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: quayio-pull-through-cache-icsp
spec:
  repositoryDigestMirrors:
  - mirrors:
    - fake
    source: quay.io
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := imageregistryv1.AddToScheme(scheme); err != nil {
				t.Fatal("add routev1 to scheme")
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&tc.config).Build()
			kubeClientGetter := func() (ctrlruntimeclient.Client, error) { return c, nil }
			step := NewQuayioPullThroughCacheStep(logrus.NewEntry(logrus.StandardLogger()),
				&tc.clusterInstall, kubeClientGetter)
			var (
				pullThroughCache          string
				pullThroughCacheWritePath string
			)
			step.writeTemplate = func(name string, data []byte, perm fs.FileMode) error {
				pullThroughCacheWritePath = name
				pullThroughCache = string(data)
				return nil
			}

			err := step.Run(context.TODO())

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

			wantPullThroughCacheWritePath := path.Join(releaseRepo, "clusters/build-clusters/build99/assets/quayio-pull-through-cache-icsp.yaml")
			if pullThroughCacheWritePath != wantPullThroughCacheWritePath {
				t.Errorf("want manifests path (write) %q but got %q", wantPullThroughCacheWritePath, pullThroughCacheWritePath)
			}

			if diff := cmp.Diff(tc.wantManifest, pullThroughCache); diff != "" {
				t.Errorf("templates differs:\n%s", diff)
			}
		})
	}
}
