package steps

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/openshift/ci-tools/pkg/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLeaseProxyProvides(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		httpSrvAddr    string
		expectedParams map[string]string
	}{
		{
			name:           "Empty HTTP server addr",
			expectedParams: map[string]string{api.LeaseProxyServerURLEnvVarName: ""},
		},
		{
			name:           "Non empty HTTP server addr",
			httpSrvAddr:    "http://10.0.0.1:8080",
			expectedParams: map[string]string{api.LeaseProxyServerURLEnvVarName: "http://10.0.0.1:8080"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := LeaseProxyStep(logrus.NewEntry(&logrus.Logger{}), tc.httpSrvAddr, &http.ServeMux{}, nil, nil, nil, wait.Backoff{})

			gotParams := make(map[string]string)
			for k, f := range step.Provides() {
				v, err := f()
				if err != nil {
					t.Fatalf("get param %s: %s", k, err)
				}
				gotParams[k] = v
			}

			if diff := cmp.Diff(tc.expectedParams, gotParams); diff != "" {
				t.Errorf("unexpected params: %s", diff)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                  string
		newLeaseProxyStepFunc func() api.Step
		wantErr               error
	}{
		{
			name: "Validation passes",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: &http.ServeMux{}, srvAddr: "x.y.w.z"}
			},
		},
		{
			name: "http mux is missing",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: nil, srvAddr: "x.y.w.z"}
			},
			wantErr: errors.New("lease proxy server requires an HTTP server mux"),
		},
		{
			name: "http address is empty",
			newLeaseProxyStepFunc: func() api.Step {
				return &stepLeaseProxyServer{logger: nil, srvMux: &http.ServeMux{}, srvAddr: ""}
			},
			wantErr: errors.New("lease proxy server requires an HTTP server address"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotErr := tc.newLeaseProxyStepFunc().Validate()

			cmpError(t, tc.wantErr, gotErr)
		})
	}
}

func TestEnsureScriptConfigMap(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(corev1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatalf("add to scheme: %s", err)
	}

	for _, tc := range []struct {
		name             string
		src              types.NamespacedName
		dst              types.NamespacedName
		objects          []ctrlruntimeclient.Object
		interceptorsFunc func() interceptor.Funcs
		wantErr          error
		wantConfigMaps   []corev1.ConfigMap
	}{
		{
			name: "Create configmap from scratch",
			src:  types.NamespacedName{Namespace: "ci", Name: "lease-proxy"},
			dst:  types.NamespacedName{Namespace: "ci-op-xxx", Name: "lease-proxy"},
			objects: []ctrlruntimeclient.Object{&corev1.ConfigMap{
				ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
				Data:       map[string]string{"foo": "bar"},
			}},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"foo": "bar"},
				},
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"foo": "bar"},
					Immutable:  ptr.To(true),
				},
			},
		},
		{
			name: "Update stale configmap",
			src:  types.NamespacedName{Namespace: "ci", Name: "lease-proxy"},
			dst:  types.NamespacedName{Namespace: "ci-op-xxx", Name: "lease-proxy"},
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
			},
		},
		{
			name: "Retry on deletion failure",
			src:  types.NamespacedName{Namespace: "ci", Name: "lease-proxy"},
			dst:  types.NamespacedName{Namespace: "ci-op-xxx", Name: "lease-proxy"},
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			interceptorsFunc: func() interceptor.Funcs {
				deleted := atomic.Bool{}
				return interceptor.Funcs{
					Delete: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
						if _, ok := obj.(*corev1.ConfigMap); ok && deleted.CompareAndSwap(false, true) {
							return &apierrors.StatusError{ErrStatus: v1.Status{Reason: v1.StatusReasonNotFound}}
						}
						return client.Delete(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
			},
		},
		{
			name: "Retry when it fails to create the up-to-date configmap",
			src:  types.NamespacedName{Namespace: "ci", Name: "lease-proxy"},
			dst:  types.NamespacedName{Namespace: "ci-op-xxx", Name: "lease-proxy"},
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			interceptorsFunc: func() interceptor.Funcs {
				createCount := atomic.Int32{}
				return interceptor.Funcs{
					Create: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
						// Fail when `create` is called for the second time. This means the stale configmap
						// has been deleted and the new one, with the up-to-date content, is about to be created.
						if _, ok := obj.(*corev1.ConfigMap); ok && createCount.Add(1) == 2 {
							return &apierrors.StatusError{ErrStatus: v1.Status{Reason: v1.StatusReasonAlreadyExists}}
						}
						return client.Create(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy", ResourceVersion: "1"},
					Data:       map[string]string{"super": "duper"},
					Immutable:  ptr.To(true),
				},
			},
		},
		{
			name: "Give up retrying due to create failing consistently",
			src:  types.NamespacedName{Namespace: "ci", Name: "lease-proxy"},
			dst:  types.NamespacedName{Namespace: "ci-op-xxx", Name: "lease-proxy"},
			objects: []ctrlruntimeclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy"},
					Data:       map[string]string{"super": "duper"},
				},
				&corev1.ConfigMap{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			interceptorsFunc: func() interceptor.Funcs {
				return interceptor.Funcs{
					Create: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
						if _, ok := obj.(*corev1.ConfigMap); ok {
							return errors.New("permafailing")
						}
						return client.Create(ctx, obj, opts...)
					},
				}
			},
			wantConfigMaps: []corev1.ConfigMap{
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"super": "duper"},
				},
				{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci-op-xxx", Name: "lease-proxy", ResourceVersion: "999"},
					Data:       map[string]string{"foo": "bar"},
				},
			},
			wantErr: errors.New("create configmap ci-op-xxx/lease-proxy: permafailing"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.interceptorsFunc == nil {
				tc.interceptorsFunc = func() interceptor.Funcs { return interceptor.Funcs{} }
			}

			client := fake.NewClientBuilder().
				WithObjects(tc.objects...).
				WithScheme(scheme).
				WithInterceptorFuncs(tc.interceptorsFunc()).
				Build()

			jobSpec := api.JobSpec{}
			jobSpec.SetNamespace("ci-op-xxx")
			logger := logrus.NewEntry(&logrus.Logger{})
			step := LeaseProxyStep(logger, "", nil, nil, client, &jobSpec, wait.Backoff{Steps: 3}).(*stepLeaseProxyServer)

			gotErr := step.EnsureScriptConfigMap(context.TODO(), tc.src, tc.dst)
			cmpError(t, tc.wantErr, gotErr)

			gotCMs := corev1.ConfigMapList{}
			if err := client.List(context.TODO(), &gotCMs, &ctrlruntimeclient.ListOptions{}); err != nil {
				t.Fatalf("list configmaps: %s", err)
			}

			if diff := cmp.Diff(tc.wantConfigMaps, gotCMs.Items); diff != "" {
				t.Errorf("unexpected configmaps: %s", diff)
			}
		})
	}
}

func cmpError(t *testing.T, want, got error) {
	if got != nil && want == nil {
		t.Errorf("want err nil but got: %v", got)
	}
	if got == nil && want != nil {
		t.Errorf("want err %v but nil", want)
	}
	if got != nil && want != nil {
		if diff := cmp.Diff(want.Error(), got.Error()); diff != "" {
			t.Errorf("unexpected error: %s", diff)
		}
	}
}
