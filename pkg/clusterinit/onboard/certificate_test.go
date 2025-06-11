package onboard

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	"github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestGenerateCertificate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		clusterInstall clusterinstall.ClusterInstall
		config         imageregistryv1.Config
		objects        []runtime.Object
		wantManifests  map[string][]interface{}
		wantErr        error
	}{
		{
			name: "Generate certificates successfully",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: "/release/repo",
					OSD:         ptr.To(false),
					Unmanaged:   ptr.To(false),
					Hosted:      ptr.To(false),
				},
				InstallConfig: types.InstallConfig{BaseDomain: "ci.devcluster.openshift.com"},
			},
			objects: []runtime.Object{
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cert-manager/certificate.yaml": {
					map[string]interface{}{
						"kind": "Certificate",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"platform": "aws",
								"project":  "openshift-ci-infra",
							},
							"name":      "apiserver-tls",
							"namespace": "openshift-config",
						},
						"spec": map[string]interface{}{
							"dnsNames": []interface{}{
								"api.build99.ci.devcluster.openshift.com",
							},
							"issuerRef": map[string]interface{}{
								"kind": "ClusterIssuer",
								"name": "cert-issuer-aws",
							},
							"secretName": "apiserver-tls",
						},
						"apiVersion": "cert-manager.io/v1",
					},
					map[string]interface{}{
						"apiVersion": "cert-manager.io/v1",
						"kind":       "Certificate",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"platform": "aws",
								"project":  "openshift-ci-infra",
							},
							"name":      "apps-tls",
							"namespace": "openshift-ingress",
						},
						"spec": map[string]interface{}{
							"dnsNames": []interface{}{
								"*.apps.build99.ci.devcluster.openshift.com",
							},
							"issuerRef": map[string]interface{}{
								"kind": "ClusterIssuer",
								"name": "cert-issuer-aws",
							},
							"secretName": "apps-tls",
						},
					},
					map[string]interface{}{
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
								"registry.build99.ci.openshift.org",
							},
							"issuerRef": map[string]interface{}{
								"kind": "ClusterIssuer",
								"name": "cert-issuer",
							},
							"secretName": "public-route-tls",
						},
					},
				},
			},
		},
		{
			name: "Non OCP generates image registry certificate only",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: "/release/repo",
					OSD:         ptr.To(true),
					Unmanaged:   ptr.To(false),
					Hosted:      ptr.To(false),
				},
				InstallConfig: types.InstallConfig{BaseDomain: "ci.devcluster.openshift.com"},
			},
			objects: []runtime.Object{
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cert-manager/certificate.yaml": {
					map[string]interface{}{
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
								"registry.build99.ci.openshift.org",
							},
							"issuerRef": map[string]interface{}{
								"kind": "ClusterIssuer",
								"name": "cert-issuer",
							},
							"secretName": "public-route-tls",
						},
					},
				},
			},
		},
		{
			name: "Override image registry hostname",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: "/release/repo",
					OSD:         ptr.To(true),
					Unmanaged:   ptr.To(false),
					Hosted:      ptr.To(false),
					Certificate: clusterinstall.Certificate{
						ImageRegistryPublicHost: "registry.overridden.ci.openshift.org",
					},
				},
				InstallConfig: types.InstallConfig{BaseDomain: "ci.devcluster.openshift.com"},
			},
			objects: []runtime.Object{
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
					}},
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cert-manager/certificate.yaml": {
					map[string]interface{}{
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
								"registry.overridden.ci.openshift.org",
							},
							"issuerRef": map[string]interface{}{
								"kind": "ClusterIssuer",
								"name": "cert-issuer",
							},
							"secretName": "public-route-tls",
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
			if err := imagev1.AddToScheme(scheme); err != nil {
				t.Fatal("add imagev1 to scheme")
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objects...).Build()
			generator := NewCertificateGenerator(&tc.clusterInstall, kubeClient)
			manifests, err := generator.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but got nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.wantManifests, manifests); diff != "" {
				t.Errorf("templates differs:\n%s", diff)
			}
		})
	}
}
