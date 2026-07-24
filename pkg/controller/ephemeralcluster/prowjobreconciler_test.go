package ephemeralcluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/steps"
	ctrlruntimetest "github.com/openshift/ci-tools/pkg/testhelper/kubernetes/ctrlruntime"
)

// pjObjectMock exists because the ProwJob object doesn't implement the Object interface.
type pjObjectMock struct {
	ctrlclient.Object
	labels map[string]string
}

func (m *pjObjectMock) GetLabels() map[string]string {
	return m.labels
}

func TestProwJobFilter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		obj        ctrlclient.Object
		wantResult bool
	}{
		{
			name:       "Label set, process",
			obj:        &pjObjectMock{labels: map[string]string{EphemeralClusterLabel: ""}},
			wantResult: true,
		},
		{
			name: "Label not set, do not process",
			obj:  &pjObjectMock{labels: map[string]string{"": ""}},
		},
		{
			name: "No labels, do not process",
			obj:  &pjObjectMock{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotResult := ProwJobFilter(tc.obj)
			if tc.wantResult != gotResult {
				t.Errorf("want %t but got %t", tc.wantResult, gotResult)
			}
		})
	}
}

func TestReconcileProwJob(t *testing.T) {
	addObjs := func(bldr *fake.ClientBuilder, objs ...ctrlclient.Object) *fake.ClientBuilder {
		for _, obj := range objs {
			if !reflect.ValueOf(obj).IsNil() {
				bldr = bldr.WithObjects(obj)
			}
		}
		return bldr
	}
	addStatusSubresourceObjs := func(bldr *fake.ClientBuilder, objs ...ctrlclient.Object) *fake.ClientBuilder {
		for _, obj := range objs {
			if !reflect.ValueOf(obj).IsNil() {
				bldr = bldr.WithStatusSubresource(obj)
			}
		}
		return bldr
	}

	fakeNow := fakeNow(t)
	scheme := fakeScheme(t)

	for _, tc := range []struct {
		name         string
		pj           *prowv1.ProwJob
		ec           *ephemeralclusterv1.EphemeralCluster
		interceptors interceptor.Funcs
		buildClients func() map[string]*ctrlruntimetest.FakeClient
		wantEC       *ephemeralclusterv1.EphemeralCluster
		wantPJ       *prowv1.ProwJob
		wantSecret   *corev1.Secret
		wantRes      reconcile.Result
		wantErr      error
	}{
		{
			name: "No EphemeralCluster label, stop reconciling",
			ec:   &ephemeralclusterv1.EphemeralCluster{},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar"},
			},
			wantEC:  &ephemeralclusterv1.EphemeralCluster{},
			wantRes: reconcile.Result{},
			wantErr: reconcile.TerminalError(errors.New("foo doesn't have the EC label")),
		},
		{
			name: "EphemeralCluster not found, ci-operator NS not found, abort ProwJob",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{"build01": ctrlruntimetest.NewFakeClient(c, scheme)}
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{
					State:          prowv1.AbortedState,
					Description:    AbortECNotFound,
					CompletionTime: ptr.To(metav1.NewTime(fakeNow)),
				},
			},
		},
		{
			name: "EphemeralCluster not found, ProwJob already completed, no-op",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec:   prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{State: prowv1.SuccessState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec:   prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{State: prowv1.SuccessState},
			},
		},
		{
			name: "EphemeralCluster not found, ProwJob already aborted, no-op",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec:   prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{State: prowv1.AbortedState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec:   prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{State: prowv1.AbortedState},
			},
		},
		{
			name: "Gracefully terminate ci-operator",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{steps.LabelJobID: "foo"},
						Name:   "ci-op-1234",
					},
				}}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			wantSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      api.EphemeralClusterTestDoneSignalSecretName,
					Namespace: "ci-op-1234",
				},
			},
		},
		{
			name: "Gracefully terminate ci-operator: secret exists already, do not create",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "foo"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      api.EphemeralClusterTestDoneSignalSecretName,
							Namespace: "ci-op-1234",
						},
						Data: map[string][]byte{"foo": []byte("bar")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			wantSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      api.EphemeralClusterTestDoneSignalSecretName,
					Namespace: "ci-op-1234",
				},
				Data: map[string][]byte{"foo": []byte("bar")},
			},
		},
		{
			name: "Build client not found returns a terminal error",
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterLabel: "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				return map[string]*ctrlruntimetest.FakeClient{}
			},
			wantErr: reconcile.TerminalError(errors.New("unknown cluster build01")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bldr := fake.NewClientBuilder().
				WithInterceptorFuncs(tc.interceptors).
				WithScheme(scheme)
			bldr = addObjs(bldr, tc.pj, tc.ec)
			client := addStatusSubresourceObjs(bldr, tc.ec).Build()

			clients := make(map[string]ctrlclient.Client)
			if tc.buildClients != nil {
				for cluster, c := range tc.buildClients() {
					clients[cluster] = c.WithWatch
				}
			}

			r := prowJobReconciler{
				logger:       logrus.NewEntry(logrus.StandardLogger()),
				masterClient: client,
				buildClients: clients,
				now:          func() time.Time { return fakeNow },
			}

			req := reconcile.Request{}
			if tc.pj != nil {
				req = reconcile.Request{NamespacedName: types.NamespacedName{Name: tc.pj.Name, Namespace: tc.pj.Namespace}}
			}
			gotRes, gotErr := r.Reconcile(context.TODO(), req)
			cmpError(t, tc.wantErr, gotErr)

			if diff := cmp.Diff(tc.wantRes, gotRes); diff != "" {
				t.Errorf("unexpected reconcile.Result: %s", diff)
			}

			if tc.wantEC != nil {
				gotEC := ephemeralclusterv1.EphemeralCluster{}
				if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.ec.Namespace, Name: tc.ec.Name}, &gotEC); err != nil {
					t.Errorf("get ephemeralcluster error: %s", err)
				}

				ignoreFields := cmpopts.IgnoreFields(ephemeralclusterv1.EphemeralCluster{}, "ResourceVersion")
				if diff := cmp.Diff(tc.wantEC, &gotEC, ignoreFields); diff != "" {
					t.Errorf("unexpected ephemeralcluster: %s", diff)
				}
			}

			if tc.wantPJ != nil {
				gotPJ := prowv1.ProwJob{}
				if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.pj.Namespace, Name: tc.pj.Name}, &gotPJ); err != nil {
					t.Errorf("get prowjob error: %s", err)
				}

				ignoreFields := cmpopts.IgnoreFields(prowv1.ProwJob{}, "ResourceVersion")
				if diff := cmp.Diff(tc.wantPJ, &gotPJ, ignoreFields); diff != "" {
					t.Errorf("unexpected prowjob: %s", diff)
				}
			}

			if tc.wantSecret != nil {
				gotSecrets := &corev1.SecretList{}
				client := clients[tc.pj.Spec.Cluster]
				if err := client.List(context.TODO(), gotSecrets); err != nil {
					t.Errorf("get secrets: %s", err)
				}

				if len(gotSecrets.Items) == 1 {
					gotSecret := gotSecrets.Items[0]
					ignoreFields := cmpopts.IgnoreFields(corev1.Secret{}, "ResourceVersion")
					if diff := cmp.Diff(tc.wantSecret, &gotSecret, ignoreFields); diff != "" {
						t.Errorf("unexpected secret: %s", diff)
					}
				} else {
					t.Errorf("expected 1 secret, got %d", len(gotSecrets.Items))
				}
			}
		})
	}
}
