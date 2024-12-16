package certmanager

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/kubernetes/portforward"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
)

func TestGenerateMafests(t *testing.T) {
	portForwarder := func(method string, url *url.URL, readyChannel chan struct{}, opts portforward.PortForwardOptions) error {
		defer close(readyChannel)
		return nil
	}
	grpcClientConnFactory := func(target string, opts ...grpc.DialOption) (conn *grpc.ClientConn, err error) {
		return nil, nil
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %s", err)
	}

	for _, tc := range []struct {
		name               string
		queryRedHatCatalog func(context.Context, GRPCClientConnFactory, string) (*Package, error)
		ci                 *clusterinstall.ClusterInstall
		rhCatalogPod       *corev1.Pod
		wantManifests      map[string][]interface{}
		wantErr            error
	}{
		{
			name: "Generate manifests successfully",
			queryRedHatCatalog: func(ctx context.Context, gcf GRPCClientConnFactory, s string) (*Package, error) {
				return &Package{
					Channels: []Channel{{
						Name:    "stable-v1",
						CSVName: "foobar-v0.0.0",
					}},
					DefaultChannelName: "stable-v1",
				}, nil
			},
			ci: &clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard:     clusterinstall.Onboard{ReleaseRepo: "/release/repo", Hosted: ptr.To(false), OSD: ptr.To(false), Unmanaged: ptr.To(false)},
				Config:      &rest.Config{},
			},
			rhCatalogPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{Namespace: OpenshiftMarketplaceNS, Labels: map[string]string{"olm.catalogSource": "redhat-operators"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{{ContainerPort: RegistryCatalogPortInt}}}}},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cert-manager-operator/operator.yaml": operatorManifests("stable-v1", "foobar-v0.0.0"),
			},
		},
		{
			name: "Query catalog error",
			queryRedHatCatalog: func(ctx context.Context, gcf GRPCClientConnFactory, s string) (*Package, error) {
				return nil, errors.New("package not found")
			},
			ci: &clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard:     clusterinstall.Onboard{ReleaseRepo: "/release/repo", Hosted: ptr.To(false), OSD: ptr.To(false), Unmanaged: ptr.To(false)},
				Config:      &rest.Config{},
			},
			rhCatalogPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{Namespace: OpenshiftMarketplaceNS, Labels: map[string]string{"olm.catalogSource": "redhat-operators"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{{ContainerPort: RegistryCatalogPortInt}}}}},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			wantErr: errors.New("query catalog: package not found"),
		},
		{
			name: "Port forward error",
			queryRedHatCatalog: func(ctx context.Context, gcf GRPCClientConnFactory, s string) (*Package, error) {
				return &Package{
					Channels: []Channel{{
						Name:    "stable-v1",
						CSVName: "foobar-v0.0.0",
					}},
					DefaultChannelName: "stable-v1",
				}, nil
			},
			ci: &clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard:     clusterinstall.Onboard{ReleaseRepo: "/release/repo", Hosted: ptr.To(false), OSD: ptr.To(false), Unmanaged: ptr.To(false)},
				Config:      &rest.Config{},
			},
			rhCatalogPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{Namespace: OpenshiftMarketplaceNS, Labels: map[string]string{"olm.catalogSource": "redhat-operators"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{{ContainerPort: RegistryCatalogPortInt}}}}},
				Status:     corev1.PodStatus{Phase: corev1.PodPending},
			},
			wantErr: errors.New("port forward: pod is not running - current status=Pending"),
		},
		{
			name: "Not an OCP, won't generate any manifest",
			ci: &clusterinstall.ClusterInstall{
				Onboard: clusterinstall.Onboard{Hosted: ptr.To(false), OSD: ptr.To(true), Unmanaged: ptr.To(false)},
			},
			rhCatalogPod:  &corev1.Pod{},
			wantManifests: map[string][]interface{}{},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()

			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.rhCatalogPod).Build()
			generator := NewGenerator(tc.ci, kubeClient, portForwarder, grpcClientConnFactory)
			generator.queryRedHatCatalog = tc.queryRedHatCatalog
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
