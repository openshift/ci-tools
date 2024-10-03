package onboard

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestGenerateCertificate(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tc := range []struct {
		name           string
		clusterInstall clusterinstall.ClusterInstall
		config         imageregistryv1.Config
		objects        []runtime.Object
		wantManifest   string
		wantErr        error
	}{
		{
			name: "Generate certificates successfully",
			clusterInstall: clusterinstall.ClusterInstall{ClusterName: "build99", Onboard: clusterinstall.Onboard{
				ReleaseRepo: "/release/repo",
				OSD:         ptr.To(false),
				Unmanaged:   ptr.To(false),
				Hosted:      ptr.To(false),
			}},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
					Data:       map[string]string{"install-config": `baseDomain: "ci.devcluster.openshift.com"`},
				},
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantManifest: `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    aws-project: openshift-ci-infra
  name: apiserver-tls
  namespace: openshift-config
spec:
  dnsNames:
  - api.build99.ci.devcluster.openshift.com
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer-aws
  secretName: apiserver-tls
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    aws-project: openshift-ci-infra
  name: apps-tls
  namespace: openshift-ingress
spec:
  dnsNames:
  - '*.apps.build99.ci.devcluster.openshift.com'
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer-aws
  secretName: apps-tls
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    gcp-project: openshift-ci-infra
  name: registry-tls
  namespace: openshift-image-registry
spec:
  dnsNames:
  - registry.build99.ci.openshift.org
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer
  secretName: public-route-tls
`,
		},
		{
			name: "Override from config",
			clusterInstall: clusterinstall.ClusterInstall{ClusterName: "build99", Onboard: clusterinstall.Onboard{
				ReleaseRepo: "/release/repo",
				OSD:         ptr.To(false),
				Unmanaged:   ptr.To(false),
				Hosted:      ptr.To(false),
				Certificate: clusterinstall.Certificate{
					BaseDomains:             "fake-domain",
					ImageRegistryPublicHost: "fake-public-host",
					ClusterIssuer:           map[string]string{"apiserver-tls": "overridden"},
					ProjectLabel:            map[string]clusterinstall.CertificateProjectLabel{"apps-tls": {Key: "foo-project", Value: "bar"}},
				},
			}},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
					Data:       map[string]string{"install-config": `baseDomain: "ci.devcluster.openshift.com"`},
				},
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantManifest: `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    aws-project: openshift-ci-infra
  name: apiserver-tls
  namespace: openshift-config
spec:
  dnsNames:
  - api.build99.fake-domain
  issuerRef:
    kind: ClusterIssuer
    name: overridden
  secretName: apiserver-tls
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    foo-project: bar
  name: apps-tls
  namespace: openshift-ingress
spec:
  dnsNames:
  - '*.apps.build99.fake-domain'
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer-aws
  secretName: apps-tls
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    gcp-project: openshift-ci-infra
  name: registry-tls
  namespace: openshift-image-registry
spec:
  dnsNames:
  - fake-public-host
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer
  secretName: public-route-tls
`,
		},
		{
			name: "Non OCP generates image registry certificate only",
			clusterInstall: clusterinstall.ClusterInstall{ClusterName: "build99", Onboard: clusterinstall.Onboard{
				ReleaseRepo: "/release/repo",
				OSD:         ptr.To(true),
				Unmanaged:   ptr.To(false),
				Hosted:      ptr.To(false),
			}},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
					Data:       map[string]string{"install-config": `baseDomain: "ci.devcluster.openshift.com"`},
				},
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantManifest: `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  labels:
    gcp-project: openshift-ci-infra
  name: registry-tls
  namespace: openshift-image-registry
spec:
  dnsNames:
  - registry.build99.ci.openshift.org
  issuerRef:
    kind: ClusterIssuer
    name: cert-issuer
  secretName: public-route-tls
`,
		},
		{
			name: "No public image registry host",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: "/release/repo",
					OSD:         ptr.To(false),
					Unmanaged:   ptr.To(false),
					Hosted:      ptr.To(false),
				},
			},
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "cluster-config-v1"},
					Data:       map[string]string{"install-config": `baseDomain: "ci.devcluster.openshift.com"`},
				},
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
					}},
				},
			},
			wantErr: errors.New("image registry public host: no public registry host could be located"),
		},
		{
			name:           "No install-config.yaml",
			clusterInstall: clusterinstall.ClusterInstall{ClusterName: "build99", Onboard: clusterinstall.Onboard{ReleaseRepo: "/release/repo"}},
			objects: []runtime.Object{
				&imagev1.ImageStreamList{
					Items: []imagev1.ImageStream{{
						ObjectMeta: metav1.ObjectMeta{Namespace: "openshift", Name: "cli"},
						Status:     imagev1.ImageStreamStatus{PublicDockerImageRepository: "registry.build99.ci.openshift.org/openshift/cli"},
					}},
				},
			},
			wantErr: errors.New(`base domain: get cluster-config-v1: configmaps "cluster-config-v1" not found`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scheme := runtime.NewScheme()
			if err := imageregistryv1.AddToScheme(scheme); err != nil {
				t.Fatal("add routev1 to scheme")
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal("add corev1 to scheme")
			}
			if err := imagev1.AddToScheme(scheme); err != nil {
				t.Fatal("add imagev1 to scheme")
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objects...).Build()
			step := NewCertificateStep(logrus.NewEntry(logrus.StandardLogger()), &tc.clusterInstall, kubeClient)
			var (
				manifests          string
				manifestsWritePath string
			)
			step.writeManifest = func(name string, data []byte, perm fs.FileMode) error {
				manifestsWritePath = name
				manifests = string(data)
				return nil
			}

			err := step.Run(context.TODO())

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

			wantManifestsWritePath := path.Join(releaseRepo, "clusters/build-clusters/build99/cert-manager/certificate.yaml")
			if manifestsWritePath != wantManifestsWritePath {
				t.Errorf("want manifests path (write) %q but got %q", wantManifestsWritePath, manifestsWritePath)
			}

			if diff := cmp.Diff(tc.wantManifest, manifests); diff != "" {
				t.Errorf("templates differs:\n%s", diff)
			}
		})
	}
}
