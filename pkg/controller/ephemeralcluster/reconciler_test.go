package ephemeralcluster

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pjutil"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakeGCSUploader struct {
	err error
}

func (u *fakeGCSUploader) UploadConfigSpec(context.Context, string, string) (string, error) {
	if u.err != nil {
		return "", u.err
	}
	return "gs://fake/gcs/path", nil
}

func newPresubmitFaker(name string, now time.Time) NewPresubmitFunc {
	return func(pr github.PullRequest, baseSHA string, job prowconfig.Presubmit, eventGUID string, additionalLabels map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob {
		pj := pjutil.NewPresubmit(pr, baseSHA, job, eventGUID, additionalLabels, modifiers...)
		pj.Name = name
		pj.Status.StartTime = v1.NewTime(now)
		return pj
	}
}

func fakeNow(t *testing.T) time.Time {
	fakeNow, err := time.Parse("2006-01-02 15:04:05", "2025-04-02 12:12:12")
	if err != nil {
		t.Fatalf("parse fake now: %s", err)
	}
	return fakeNow
}

func fakeScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(ephemeralclusterv1.AddToScheme, prowv1.AddToScheme, corev1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatal("build scheme")
	}
	return scheme
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

func TestCreateProwJob(t *testing.T) {
	fakeNow := fakeNow(t)
	scheme := fakeScheme(t)
	const pollingTime = 5

	for _, tc := range []struct {
		name            string
		ec              ephemeralclusterv1.EphemeralCluster
		req             reconcile.Request
		interceptors    interceptor.Funcs
		configUploadErr error
		wantRes         reconcile.Result
		wantErr         error
	}{
		{
			name: "An EphemeralCluster request creates a ProwJob",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Workflow: ephemeralclusterv1.Workflow{
							Name: "test-workflow",
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name:            "Handle config upload error",
			configUploadErr: errors.New("upload error"),
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Workflow: ephemeralclusterv1.Workflow{
							Name: "test-workflow",
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Fail to create a ProwJob",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Workflow: ephemeralclusterv1.Workflow{
							Name: "test-workflow",
						},
					},
				},
			},
			interceptors: interceptor.Funcs{Create: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
				if _, ok := obj.(*prowv1.ProwJob); ok {
					return errors.New("fake err")
				}
				return client.Create(ctx, obj, opts...)
			}},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fake.NewClientBuilder().
				WithObjects(&tc.ec).
				WithScheme(scheme).
				WithInterceptorFuncs(tc.interceptors).
				Build()

			r := reconciler{
				logger:         logrus.NewEntry(logrus.StandardLogger()),
				masterClient:   client,
				now:            func() time.Time { return fakeNow },
				polling:        func() time.Duration { return pollingTime },
				newPresubmit:   newPresubmitFaker("foobar", fakeNow),
				configUploader: &fakeGCSUploader{err: tc.configUploadErr},
			}

			gotRes, gotErr := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: tc.ec.Name, Namespace: tc.ec.Namespace}})
			cmpError(t, tc.wantErr, gotErr)

			if diff := cmp.Diff(tc.wantRes, gotRes); diff != "" {
				t.Errorf("unexpected reconcile.Result: %s", diff)
			}

			gotEC := ephemeralclusterv1.EphemeralCluster{}
			if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.ec.Namespace, Name: tc.ec.Name}, &gotEC); err != nil {
				t.Errorf("unexpected get ephemeralcluster error: %s", err)
			}

			testhelper.CompareWithFixture(t, gotEC, testhelper.WithPrefix("ec-"))

			pjs := prowv1.ProwJobList{}
			if err := client.List(context.TODO(), &pjs); err != nil {
				t.Errorf("unexpected list pj error: %s", err)
			}

			testhelper.CompareWithFixture(t, pjs, testhelper.WithPrefix("pj-"))
		})
	}
}

func TestReconcile(t *testing.T) {
	scheme := fakeScheme(t)
	fakeNow := fakeNow(t)
	const pollingTime = 5

	for _, tc := range []struct {
		name             string
		ec               *ephemeralclusterv1.EphemeralCluster
		objs             []ctrlclient.Object
		buildClients     func() map[string]ctrlclient.Client
		buildClusterObjs []ctrlclient.Object
		wantEC           *ephemeralclusterv1.EphemeralCluster
		wantRes          reconcile.Result
		wantErr          error
	}{
		{
			name: "Kubeconfig stored already, do nothing",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "kubeconfig",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "999",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "kubeconfig",
				},
			},
			wantRes: reconcile.Result{},
		},
		{
			name: "Kubeconfig ready",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]ctrlclient.Client {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: WaitTestStepName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				return map[string]ctrlclient.Client{
					"build01": fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build(),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "kubeconfig",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
		},
		{
			name: "ci-operator NS doesn't exist yet",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]ctrlclient.Client {
				return map[string]ctrlclient.Client{
					"build01": fake.NewClientBuilder().WithScheme(scheme).Build(),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Secret not found",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]ctrlclient.Client {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
				}
				return map[string]ctrlclient.Client{
					"build01": fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build(),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            fmt.Sprintf("secrets %q not found", WaitTestStepName),
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Kubeconfig not ready",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]ctrlclient.Client {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: WaitTestStepName, Namespace: "ci-op-1234"},
					},
				}
				return map[string]ctrlclient.Client{
					"build01": fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build(),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            ephemeralclusterv1.KubeconfigNotReadMsg,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Client not found, return a terminal error",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]ctrlclient.Client { return map[string]ctrlclient.Client{} },
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            "uknown cluster build01",
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
			wantErr: reconcile.TerminalError(errors.New("uknown cluster build01")),
		},
		{
			name: "Aborted ProwJob maps to ProwJobCompleted condition",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "k",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
				},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "k",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						Reason:             ephemeralclusterv1.ProwJobFailureReason,
						Message:            "prowjob state: aborted",
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Succeeded ProwJob maps to ProwJobCompleted condition",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "k",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
					Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID:  "pj-123",
					Kubeconfig: "k",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						Reason:             string(ephemeralclusterv1.ProwJobCompleted),
						Message:            "prowjob state: success",
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "ProwJob not found remove the finalizer",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:       "foo",
					Namespace:  "bar",
					Finalizers: []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantRes: reconcile.Result{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := fake.NewClientBuilder().
				WithObjects(tc.ec).
				WithObjects(tc.objs...).
				WithScheme(scheme).
				Build()

			clients := make(map[string]ctrlclient.Client)
			if tc.buildClients != nil {
				clients = tc.buildClients()
			}

			r := reconciler{
				logger:         logrus.NewEntry(logrus.StandardLogger()),
				masterClient:   client,
				buildClients:   clients,
				now:            func() time.Time { return fakeNow },
				polling:        func() time.Duration { return pollingTime },
				newPresubmit:   newPresubmitFaker("foobar", fakeNow),
				configUploader: &fakeGCSUploader{err: nil},
			}

			gotRes, gotErr := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: tc.ec.Name, Namespace: tc.ec.Namespace}})
			cmpError(t, tc.wantErr, gotErr)

			if diff := cmp.Diff(tc.wantRes, gotRes); diff != "" {
				t.Errorf("unexpected reconcile.Result: %s", diff)
			}

			gotEC := ephemeralclusterv1.EphemeralCluster{}
			if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.ec.Namespace, Name: tc.ec.Name}, &gotEC); err != nil {
				t.Errorf("unexpected get ephemeralcluster error: %s", err)
			}

			ignoreFields := cmpopts.IgnoreFields(ephemeralclusterv1.EphemeralCluster{}, "ResourceVersion")
			if diff := cmp.Diff(tc.wantEC, &gotEC, ignoreFields); diff != "" {
				t.Errorf("unexpected ephemeralcluster: %s", diff)
			}
		})
	}
}

func TestDeleteProwJob(t *testing.T) {
	scheme := fakeScheme(t)
	fakeNow := fakeNow(t)
	const pollingTime = 5

	for _, tc := range []struct {
		name    string
		ec      *ephemeralclusterv1.EphemeralCluster
		pj      *prowv1.ProwJob
		wantEC  *ephemeralclusterv1.EphemeralCluster
		wantPJ  *prowv1.ProwJob
		wantRes reconcile.Result
		wantErr error
	}{
		{
			name: "Delete EC: abort the ProwJob",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(v1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(v1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: ProwJobNamespace},
				Status: prowv1.ProwJobStatus{
					State:          prowv1.AbortedState,
					Description:    AbortProwJobDeleteEC,
					CompletionTime: ptr.To(v1.NewTime(fakeNow)),
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Delete EC: ProwJob is gone already, remove the finalizer and delete EC",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(v1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantRes: reconcile.Result{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bldr := fake.NewClientBuilder().
				WithObjects(tc.ec).
				WithScheme(scheme)

			if tc.pj != nil {
				bldr = bldr.WithObjects(tc.pj)
			}

			client := bldr.Build()

			r := reconciler{
				logger:       logrus.NewEntry(logrus.StandardLogger()),
				masterClient: client,
				buildClients: map[string]ctrlclient.Client{},
				now:          func() time.Time { return fakeNow },
				polling:      func() time.Duration { return pollingTime },
			}

			gotRes, gotErr := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: tc.ec.Name, Namespace: tc.ec.Namespace}})
			cmpError(t, tc.wantErr, gotErr)

			if diff := cmp.Diff(tc.wantRes, gotRes); diff != "" {
				t.Errorf("unexpected reconcile.Result: %s", diff)
			}

			if tc.wantEC != nil {
				gotEC := ephemeralclusterv1.EphemeralCluster{}
				if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.ec.Namespace, Name: tc.ec.Name}, &gotEC); err != nil {
					t.Errorf("unexpected get ephemeralcluster error: %s", err)
				}

				ignoreFields := cmpopts.IgnoreFields(ephemeralclusterv1.EphemeralCluster{}, "ResourceVersion")
				if diff := cmp.Diff(tc.wantEC, &gotEC, ignoreFields); diff != "" {
					t.Errorf("unexpected ephemeralcluster: %s", diff)
				}
			} else {
				ecList := ephemeralclusterv1.EphemeralClusterList{}
				err := client.List(context.TODO(), &ecList)
				if err != nil {
					t.Fatalf("list ecs: %s", err)
				}

				if len(ecList.Items) > 0 {
					t.Error("ephemeral cluster has not been deleted")
				}
			}

			if tc.wantPJ != nil {
				gotPJ := prowv1.ProwJob{}
				if err := client.Get(context.TODO(), types.NamespacedName{Namespace: tc.wantPJ.Namespace, Name: tc.wantPJ.Name}, &gotPJ); err != nil {
					t.Errorf("unexpected get ephemeralcluster error: %s", err)
				}

				ignoreFields := cmpopts.IgnoreFields(prowv1.ProwJob{}, "ResourceVersion")
				if diff := cmp.Diff(tc.wantPJ, &gotPJ, ignoreFields); diff != "" {
					t.Errorf("unexpected prowjob: %s", diff)
				}
			} else {
				pjList := prowv1.ProwJobList{}
				err := client.List(context.TODO(), &pjList)
				if err != nil {
					t.Fatalf("list pjs: %s", err)
				}

				if len(pjList.Items) > 0 {
					t.Error("prowjob has not been deleted")
				}
			}
		})
	}
}
