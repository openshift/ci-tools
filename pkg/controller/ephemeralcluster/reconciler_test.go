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

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/testhelper"
	ctrlruntimetest "github.com/openshift/ci-tools/pkg/testhelper/kubernetes/ctrlruntime"
)

const (
	prowJobNamespace = "ci"
)

func newPresubmitFaker(name string, now time.Time) NewPresubmitFunc {
	return func(pr github.PullRequest, baseSHA string, job prowconfig.Presubmit, eventGUID string, additionalLabels map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob {
		pj := pjutil.NewPresubmit(pr, baseSHA, job, eventGUID, additionalLabels, modifiers...)
		pj.Name = name
		pj.Status.StartTime = v1.NewTime(now)
		return pj
	}
}

func prowConfigAgent(c *prowconfig.Config) *prowconfig.Agent {
	a := prowconfig.Agent{}
	a.Set(c)
	return &a
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

func TestEphemeralClusterFilter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		obj        ctrlclient.Object
		wantResult bool
	}{
		{
			name:       "Namespace set, process",
			obj:        &ephemeralclusterv1.EphemeralCluster{ObjectMeta: v1.ObjectMeta{Namespace: EphemeralClusterNamespace}},
			wantResult: true,
		},
		{
			name: "Unexpected namespace, do not process",
			obj:  &ephemeralclusterv1.EphemeralCluster{ObjectMeta: v1.ObjectMeta{Namespace: "foo"}},
		},
		{
			name: "Namespace unset, do not process",
			obj:  &ephemeralclusterv1.EphemeralCluster{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotResult := ECPredicateFilter(tc.obj)
			if tc.wantResult != gotResult {
				t.Errorf("want %t but got %t", tc.wantResult, gotResult)
			}
		})
	}
}

func TestCreateProwJob(t *testing.T) {
	fakeNow := fakeNow(t)
	scheme := fakeScheme(t)
	const pollingTime = 5
	prowConfig := prowconfig.Config{
		ProwConfig: prowconfig.ProwConfig{
			ProwJobNamespace: prowJobNamespace,
			PodNamespace:     prowJobNamespace,
			InRepoConfig:     prowconfig.InRepoConfig{AllowedClusters: map[string][]string{"": {"default"}}},
			Plank: prowconfig.Plank{
				DefaultDecorationConfigs: []*prowconfig.DefaultDecorationConfigEntry{{
					Config: &prowv1.DecorationConfig{
						GCSConfiguration: &prowv1.GCSConfiguration{
							DefaultOrg:   "org",
							DefaultRepo:  "repo",
							PathStrategy: prowv1.PathStrategySingle,
						},
						UtilityImages: &prowv1.UtilityImages{
							CloneRefs:  "clonerefs",
							InitUpload: "initupload",
							Entrypoint: "entrypoint",
							Sidecar:    "sidecar",
						},
					},
				}},
			},
		},
	}

	for _, tc := range []struct {
		name         string
		ec           ephemeralclusterv1.EphemeralCluster
		pjs          []ctrlclient.Object
		req          reconcile.Request
		interceptors interceptor.Funcs
		prowConfig   *prowconfig.Config
		wantRes      reconcile.Result
		wantErr      error
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
						Releases: map[string]api.UnresolvedRelease{
							"initial": {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
							"latest":  {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
						},
						Test: ephemeralclusterv1.TestSpec{
							Workflow:       "test-workflow",
							Env:            map[string]string{"foo": "bar"},
							ClusterProfile: "aws",
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Handle invalid prow config",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Releases: map[string]api.UnresolvedRelease{
							"initial": {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
							"latest":  {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
						},
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "test-workflow",
						},
					},
				},
			},
			prowConfig: &prowconfig.Config{},
			req:        reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes:    reconcile.Result{},
			wantErr:    errors.New("terminal error: validate and default presubmit: invalid presubmit job pull-ci-org-repo-branch-cluster-provisioning: failed to default namespace"),
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
						Releases: map[string]api.UnresolvedRelease{
							"initial": {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
							"latest":  {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
						},
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "test-workflow",
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
			wantRes: reconcile.Result{},
			wantErr: errors.New("create prowjob: fake err"),
		},
		{
			name: "Invalid ci-operator configuration",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "test-workflow",
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{},
			wantErr: errors.New("terminal error: generate ci-operator config: releases stanza not set"),
		},
		{
			name: "Invalid ci-operator configuration and fail to update EC",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "test-workflow",
						},
					},
				},
			},
			interceptors: interceptor.Funcs{Update: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
				if _, ok := obj.(*ephemeralclusterv1.EphemeralCluster); ok {
					return errors.New("fake err")
				}
				return client.Update(ctx, obj, opts...)
			}},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{},
			wantErr: errors.New("[update ephemeral cluster: fake err, terminal error: generate ci-operator config: releases stanza not set]"),
		},
		{
			name: "Several PJ for the same EC raises an error",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{Namespace: "ns", Name: "ec"},
			},
			pjs: []ctrlclient.Object{
				&prowv1.ProwJob{ObjectMeta: v1.ObjectMeta{
					Labels:    map[string]string{EphemeralClusterLabel: "ec"},
					Name:      "pj1",
					Namespace: prowJobNamespace,
				}},
				&prowv1.ProwJob{ObjectMeta: v1.ObjectMeta{
					Labels:    map[string]string{EphemeralClusterLabel: "ec"},
					Name:      "pj2",
					Namespace: prowJobNamespace,
				}},
			},
			wantRes: reconcile.Result{},
			wantErr: errors.New("terminal error: too many ProwJobs associated"),
		},
		{
			name: "PJ found but was not bound to the EC",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{Namespace: "ns", Name: "ec"},
			},
			pjs: []ctrlclient.Object{&prowv1.ProwJob{ObjectMeta: v1.ObjectMeta{
				Labels:    map[string]string{EphemeralClusterLabel: "ec"},
				Name:      "pj",
				Namespace: prowJobNamespace,
			}}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clientBldr := fake.NewClientBuilder()

			if tc.pjs != nil {
				clientBldr = clientBldr.WithObjects(tc.pjs...)
			}

			client := clientBldr.
				WithObjects(&tc.ec).
				WithScheme(scheme).
				WithInterceptorFuncs(tc.interceptors).
				Build()

			pc := &prowConfig
			if tc.prowConfig != nil {
				pc = tc.prowConfig
			}

			r := reconciler{
				logger:          logrus.NewEntry(logrus.StandardLogger()),
				masterClient:    client,
				now:             func() time.Time { return fakeNow },
				polling:         func() time.Duration { return pollingTime },
				newPresubmit:    newPresubmitFaker("foobar", fakeNow),
				prowConfigAgent: prowConfigAgent(pc),
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
		name         string
		ec           *ephemeralclusterv1.EphemeralCluster
		objs         []ctrlclient.Object
		buildClients func() map[string]*ctrlruntimetest.FakeClient
		wantEC       *ephemeralclusterv1.EphemeralCluster
		wantRes      reconcile.Result
		wantErr      error
	}{
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:      ephemeralclusterv1.EphemeralClusterReady,
					ProwJobID:  "pj-123",
					Kubeconfig: "kubeconfig",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme),
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
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{
						{
							Type:               ephemeralclusterv1.ProwJobCreating,
							Status:             ephemeralclusterv1.ConditionFalse,
							Reason:             ProwJobCreatingDoneReason,
							LastTransitionTime: v1.NewTime(fakeNow),
						}, {
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
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
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            fmt.Sprintf("secrets %q not found", EphemeralClusterTestName),
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
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
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				return map[string]*ctrlruntimetest.FakeClient{}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterFailed,
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
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
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						Reason:             ephemeralclusterv1.ProwJobFailureReason,
						Message:            "prowjob state: aborted",
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
					Phase: ephemeralclusterv1.EphemeralClusterFailed,
				},
			},
			wantRes: reconcile.Result{},
		},
		{
			name: "Succeeded ProwJob maps to ProwJobCompleted condition",
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
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:      ephemeralclusterv1.EphemeralClusterDeprovisioned,
					ProwJobID:  "pj-123",
					Kubeconfig: "kubeconfig",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						Reason:             string(ephemeralclusterv1.ProwJobCompleted),
						Message:            "prowjob state: success",
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
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
		{
			name: "Test completed, create secret",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
					Kubeconfig: "kubeconfig",
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Test completed, ci-operator NS not found",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterFailed,
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.CreateTestCompletedSecretFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Test completed, secret exists do nothing",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: v1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: v1.ObjectMeta{
							Labels:    map[string]string{"do-not-change": ""},
							Name:      api.EphemeralClusterTestDoneSignalSecretName,
							Namespace: "ci-op-1234",
						},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					ProwJobID: "pj-123",
					Conditions: []ephemeralclusterv1.EphemeralClusterCondition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ProwJobCreatingDoneReason,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             ephemeralclusterv1.ConditionFalse,
						Reason:             ephemeralclusterv1.KubeconfigFetchFailureReason,
						Message:            `secrets "cluster-provisioning" not found`,
						LastTransitionTime: v1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             ephemeralclusterv1.ConditionTrue,
						LastTransitionTime: v1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
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
			fakeClients := make(map[string]*ctrlruntimetest.FakeClient)
			if tc.buildClients != nil {
				fakeClients = tc.buildClients()
				for cluster, c := range fakeClients {
					clients[cluster] = c.WithWatch
				}
			}

			r := reconciler{
				logger:       logrus.NewEntry(logrus.StandardLogger()),
				masterClient: client,
				buildClients: clients,
				now:          func() time.Time { return fakeNow },
				polling:      func() time.Duration { return pollingTime },
				newPresubmit: newPresubmitFaker("foobar", fakeNow),
				prowConfigAgent: prowConfigAgent(&prowconfig.Config{
					ProwConfig: prowconfig.ProwConfig{ProwJobNamespace: prowJobNamespace},
				}),
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

			for cluster, c := range fakeClients {
				allObjs, err := c.Objects()
				if err != nil {
					t.Fatalf("objects: %s", err)
				}

				if len(allObjs) > 0 {
					testhelper.CompareWithFixture(t, allObjs, testhelper.WithPrefix(cluster+"-"))
				}
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
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
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
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
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
		{
			name: "Aborted ProwJob remove the finalizer and delete",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: v1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(v1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{ProwJobID: "pj-123"},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
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
				prowConfigAgent: prowConfigAgent(&prowconfig.Config{
					ProwConfig: prowconfig.ProwConfig{ProwJobNamespace: prowJobNamespace},
				}),
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
