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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/testhelper"
	ctrlruntimetest "github.com/openshift/ci-tools/pkg/testhelper/kubernetes/ctrlruntime"
)

const (
	prowJobNamespace = "ci"
)

type fakeRegistryAgent struct {
	agents.RegistryAgent
	clusterProfiles map[string]*api.ClusterProfile
}

func (f *fakeRegistryAgent) ResolveClusterProfile(name string) (api.ClusterProfile, error) {
	cp, ok := f.clusterProfiles[name]
	if !ok {
		return api.ClusterProfile{}, fmt.Errorf("cluster profile %q not found", name)
	}
	return *cp, nil
}

func newProwJobFaker(name string, now time.Time) NewProwJobFunc {
	return func(spec prowv1.ProwJobSpec, extraLabels, extraAnnotations map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob {
		pj := pjutil.NewProwJob(spec, extraLabels, extraAnnotations, modifiers...)
		pj.Name = name
		pj.Status.StartTime = metav1.NewTime(now)
		return pj
	}
}

func prowConfigAgent(c *prowconfig.Config) *prowconfig.Agent {
	a := prowconfig.Agent{}
	a.Set(c)
	return &a
}

func fakeNow(t *testing.T) time.Time {
	return parseTime(t, "2025-04-02 12:12:12")
}

func cmpError(t *testing.T, want, got error) {
	if got != nil && want == nil {
		t.Errorf("want err nil but got: %v", got)
	}
	if got == nil && want != nil {
		t.Errorf("want err %v but got nil", want)
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
			obj:        &ephemeralclusterv1.EphemeralCluster{ObjectMeta: metav1.ObjectMeta{Namespace: EphemeralClusterNamespace}},
			wantResult: true,
		},
		{
			name: "Unexpected namespace, do not process",
			obj:  &ephemeralclusterv1.EphemeralCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "foo"}},
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

func TestReconcileCreateProwJob(t *testing.T) {
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

	fakeRegistryAgent := fakeRegistryAgent{
		clusterProfiles: map[string]*api.ClusterProfile{
			"aws": {
				ClusterType: "aws",
				Owners: []api.ClusterProfileOwners{{
					Konflux: &api.ClusterProfileKonfluxOwner{
						Tenant:           "ktenant",
						ClustersResolved: []string{"kcluster"},
					},
				}},
			},
			"aws-2": {
				ClusterType: "aws",
				Owners: []api.ClusterProfileOwners{{
					Konflux: &api.ClusterProfileKonfluxOwner{
						Tenant:           "ktenant",
						ClustersResolved: []string{"kcluster"},
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
		wantEvents   []string
		wantErr      error
	}{
		{
			name: "An EphemeralCluster request creates a ProwJob",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						BuildRootImage: &api.BuildRootImageConfiguration{
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "cli",
							},
						},
						BaseImages: map[string]api.ImageStreamTagReference{
							"upi-installer": {
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "upi-installer",
							},
						},
						ExternalImages: map[string]api.ExternalImage{
							"fedora": {Registry: "quay.io/fedora/fedora:43"},
						},
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
			wantEvents: []string{
				"Normal ProwJobCreated Created ProwJob foobar",
			},
		},
		{
			name: "Privileged tenant",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxTenantAnnotation: "ktenant-privileged",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						BuildRootImage: &api.BuildRootImageConfiguration{
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "cli",
							},
						},
						BaseImages: map[string]api.ImageStreamTagReference{
							"upi-installer": {
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "upi-installer",
							},
						},
						ExternalImages: map[string]api.ExternalImage{
							"fedora": {Registry: "quay.io/fedora/fedora:43"},
						},
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
			wantEvents: []string{
				"Normal ProwJobCreated Created ProwJob foobar",
			},
		},
		{
			name: "Invalid cluster profile",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "foo",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "bar",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow:       "test-workflow",
							Env:            map[string]string{"foo": "bar"},
							ClusterProfile: "aws-2",
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				`Warning ValidationFailed konflux cluster "foo" and tenant "bar" don't own the cluster profile "aws-2"`,
			},
			wantErr: errors.New(`terminal error: validate ephemeral cluster: konflux cluster "foo" and tenant "bar" don't own the cluster profile "aws-2"`),
		},
		{
			name: "Cluster profile is not set",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "foo",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "bar",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "test-workflow",
							Env:      map[string]string{"foo": "bar"},
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning ValidationFailed cluster profile has not been set",
			},
			wantErr: errors.New(`terminal error: validate ephemeral cluster: cluster profile has not been set`),
		},
		{
			name: "Hive cluster request creates a ProwJob",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						BuildRootImage: &api.BuildRootImageConfiguration{
							ImageStreamTagReference: &api.ImageStreamTagReference{
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "cli",
							},
						},
						BaseImages: map[string]api.ImageStreamTagReference{
							"cli": {
								Namespace: "ocp",
								Name:      "4.20",
								Tag:       "cli",
							},
						},
						ExternalImages: map[string]api.ExternalImage{
							"fedora": {Registry: "quay.io/fedora/fedora:43"},
						},
						Test: ephemeralclusterv1.TestSpec{
							Workflow: "generic-claim",
							Env:      map[string]string{"foo": "bar"},
							ClusterClaim: &api.ClusterClaim{
								As:           "claim",
								Product:      "ocp",
								Version:      "4.22",
								Architecture: api.ReleaseArchitectureAMD64,
								Cloud:        "aws",
								Owner:        "test-platform",
								Labels:       map[string]string{"region": "us-east-1"},
								Timeout:      &prowv1.Duration{},
							},
						},
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal ProwJobCreated Created ProwJob foobar",
			},
		},
		{
			name: "Handle invalid prow config",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
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
							ClusterProfile: "aws",
						},
					},
				},
			},
			prowConfig: &prowconfig.Config{
				JobConfig: prowconfig.JobConfig{
					Presets: []prowconfig.Preset{{
						Volumes: []corev1.Volume{{
							Name: "boskos",
						}},
					}},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
			wantRes: reconcile.Result{},
			wantErr: errors.New("terminal error: default periodic: job periodic-ci-org-repo-branch-cluster-provisioning failed to merge presets for podspec: volume duplicated in pod spec: boskos"),
		},
		{
			name: "Fail to create a ProwJob",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
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
							ClusterProfile: "aws",
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
			wantEvents: []string{
				"Warning ProwJobCreationFailed Failed to create ProwJob: create prowjob: fake err",
			},
			wantErr: errors.New("create prowjob: fake err"),
		},
		{
			name: "Invalid ci-operator configuration",
			ec: ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow:       "test-workflow",
							ClusterProfile: "aws",
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
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							Workflow:       "test-workflow",
							ClusterProfile: "aws",
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
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{CIOperator: ephemeralclusterv1.CIOperatorSpec{
					Test: ephemeralclusterv1.TestSpec{
						ClusterProfile: "aws",
					},
				}},
			},
			pjs: []ctrlclient.Object{
				&prowv1.ProwJob{ObjectMeta: metav1.ObjectMeta{
					Labels:    map[string]string{EphemeralClusterLabel: "ec"},
					Name:      "pj1",
					Namespace: prowJobNamespace,
				}},
				&prowv1.ProwJob{ObjectMeta: metav1.ObjectMeta{
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
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ephemeralclusterv1.KonfluxClusterAnnotation: "kcluster",
						ephemeralclusterv1.KonfluxTenantAnnotation:  "ktenant",
					},
					Namespace: "ns",
					Name:      "ec",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{CIOperator: ephemeralclusterv1.CIOperatorSpec{
					Test: ephemeralclusterv1.TestSpec{
						ClusterProfile: "aws",
					},
				}},
			},
			pjs: []ctrlclient.Object{&prowv1.ProwJob{ObjectMeta: metav1.ObjectMeta{
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
				WithStatusSubresource(&tc.ec).
				WithScheme(scheme).
				WithInterceptorFuncs(tc.interceptors).
				Build()

			pc := &prowConfig
			if tc.prowConfig != nil {
				pc = tc.prowConfig
			}

			fakeEventRecorder := record.NewFakeRecorder(100)
			r := reconciler{
				logger:                 logrus.NewEntry(logrus.StandardLogger()),
				masterClient:           client,
				now:                    func() time.Time { return fakeNow },
				polling:                func() time.Duration { return pollingTime },
				cliISTagRef:            api.ImageStreamTagReference{Namespace: "ocp", Name: "4.22", Tag: "cli"},
				newProwJob:             newProwJobFaker("foobar", fakeNow),
				prowConfigAgent:        prowConfigAgent(pc),
				clusterProfileResolver: clusterProfileResolverAdapter(&fakeRegistryAgent),
				privilegedTenants:      sets.New("ktenant-privileged"),
				recorder:               fakeEventRecorder,
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

			if diff := cmp.Diff(tc.wantEvents, drainEvents(fakeEventRecorder)); diff != "" {
				t.Errorf("events differ: %s\n", diff)
			}
		})
	}
}

func TestReconcile(t *testing.T) {
	scheme := fakeScheme(t)
	fakeNow := fakeNow(t)
	const pollingTime = 5

	for _, tc := range []struct {
		name                                string
		ec                                  *ephemeralclusterv1.EphemeralCluster
		objs                                []ctrlclient.Object
		buildClients                        func() map[string]*ctrlruntimetest.FakeClient
		now                                 *time.Time
		wantEC                              *ephemeralclusterv1.EphemeralCluster
		wantSecret                          *corev1.Secret
		wantRes                             reconcile.Result
		wantProvisioningDurationHistogram   []metric
		wantDeprovisioningDurationHistogram []metric
		wantEvents                          []string
		wantErr                             error
	}{
		{
			name: "Kubeconfig ready",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:12:12")),
					UID:               types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{Workflow: "e2e-aws"},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{URL: "https://pj-123.html"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-credentials",
					Namespace: "bar",
				},
				Data: map[string][]byte{
					"kubeconfig":        []byte("kubeconfig"),
					"kubeAdminPassword": {},
				},
			},
			now: ptr.To(parseTime(t, "2025-04-02 12:14:12")),
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:12:12")),
					ResourceVersion:   "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{Workflow: "e2e-aws"},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:      ephemeralclusterv1.EphemeralClusterReady,
					ProwJobID:  "pj-123",
					SecretRef:  "foo-credentials",
					ProwJobURL: "https://pj-123.html",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:14:12")),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.CredentialsReadyReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:14:12")),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal ClusterReady Cluster credentials are available",
			},
			wantProvisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{"e2e-aws"},
					SampleCount: 1,
					Buckets:     histogramBuckets(provisioningDurationBuckets, 1),
				},
			}},
		},
		{
			name: "ci-operator NS doesn't exist yet",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
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
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterProvisioning,
					Conditions: []metav1.Condition{
						{
							Type:               ephemeralclusterv1.ProwJobCreating,
							Status:             metav1.ConditionFalse,
							Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
							LastTransitionTime: metav1.NewTime(fakeNow),
						}, {
							Type:               ephemeralclusterv1.ClusterReady,
							Status:             metav1.ConditionFalse,
							Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
							Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
							LastTransitionTime: metav1.NewTime(fakeNow),
						}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Secret not found",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
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
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterProvisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            fmt.Sprintf("secrets %q not found", EphemeralClusterTestName),
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Kubeconfig not ready",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterProvisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            ephemeralclusterv1.KubeconfigNotReadyMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Client not found, abort prowjob",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				return map[string]*ctrlruntimetest.FakeClient{}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterFailed,
					ProwJobID: "pj-123",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            "build client not found for cluster build01",
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning BuildClientNotFound Build client not found, aborting ProwJob pj-123: build client not found for cluster build01",
				"Warning ProwJobAborted ProwJob pj-123 aborted: Build client not found: build client not found for cluster build01",
			},
			wantErr: reconcile.TerminalError(errors.New("build client not found for cluster build01")),
		},
		{
			name: "Aborted ProwJob maps to ProwJobCompleted condition",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
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
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.ProwJobFailureReason,
						Message:            "prowjob state: aborted",
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
					Phase: ephemeralclusterv1.EphemeralClusterFailed,
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning ProwJobFailed ProwJob pj-123 finished with state: aborted",
			},
		},
		{
			name: "Succeeded ProwJob maps to ProwJobCompleted condition",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 11:52:12")),
					UID:               types.UID("test-ec-uid"),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 11:52:12")),
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioned,
					ProwJobID: "pj-123",
					SecretRef: "foo-credentials",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.CredentialsReadyReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             string(ephemeralclusterv1.ProwJobCompletedReason),
						Message:            "prowjob state: success",
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Normal ClusterReady Cluster credentials are available",
				"Normal ProwJobSucceeded ProwJob pj-123 completed successfully",
			},
			wantProvisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{""},
					SampleCount: 1,
					Buckets:     histogramBuckets(provisioningDurationBuckets, 0, 0, 0, 1),
				},
			}},
		},
		{
			name: "ProwJob not found remove the finalizer",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "foo",
					Namespace:  "bar",
					UID:        types.UID("test-ec-uid"),
					Finalizers: []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
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
			name: "ProwJob succeeded remove the finalizer",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "foo",
					Namespace:  "bar",
					UID:        types.UID("test-ec-uid"),
					Finalizers: []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioned,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.ProwJobCompletedReason,
						Message:            "prowjob state: " + string(prowv1.SuccessState),
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Normal ProwJobSucceeded ProwJob pj-123 completed successfully",
			},
		},
		{
			name: "Test completed, create secret",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 11:57:12")),
					UID:               types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 11:57:12")),
					ResourceVersion:   "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					ProwJobID: "pj-123",
					SecretRef: "foo-credentials",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.CredentialsReadyReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.TestDoneReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal ClusterReady Cluster credentials are available",
				"Normal DeprovisioningStarted Deprovisioning signal sent",
			},
			wantProvisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{""},
					SampleCount: 1,
					Buckets:     histogramBuckets(provisioningDurationBuckets, 0, 0, 1),
				},
			}},
		},
		{
			name: "Test completed, ci-operator NS not found",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
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
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterFailed,
					ProwJobID: "pj-123",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.CreateTestCompletedSecretFailureReason,
						Message:            ephemeralclusterv1.CIOperatorNSNotFoundMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "Test completed, secret exists do nothing",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
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
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{TearDownCluster: true},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					ProwJobID: "pj-123",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            `secrets "cluster-provisioning" not found`,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.TestDoneReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal DeprovisioningStarted Deprovisioning signal sent",
			},
		},
		{
			name: "Hive cluster provisioned, report secrets",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:12:12")),
					UID:               types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: HiveKubeconfigSecret, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{api.HiveAdminKubeconfigSecretKey: []byte("kubeconfig")},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: HiveAdminPasswdSecret, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{api.HiveAdminPasswordSecretKey: []byte("admin-passwd")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-credentials",
					Namespace: "bar",
				},
				Data: map[string][]byte{
					"kubeconfig":        []byte("kubeconfig"),
					"kubeAdminPassword": []byte("admin-passwd"),
				},
			},
			now: ptr.To(parseTime(t, "2025-04-02 13:11:12")),
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:12:12")),
					ResourceVersion:   "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterReady,
					ProwJobID: "pj-123",
					SecretRef: "foo-credentials",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 13:11:12")),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.HiveCredentialsReadyReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 13:11:12")),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal ClusterReady Cluster credentials are available",
			},
			wantProvisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{""},
					SampleCount: 1,
					Buckets:     histogramBuckets(provisioningDurationBuckets, 0, 0, 0, 0, 1),
				},
			}},
		},
		{
			name: "Hive cluster not ready yet, kubeconfig missing",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: HiveAdminPasswdSecret, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{api.HiveAdminPasswordSecretKey: []byte("admin-passwd")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterProvisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            ephemeralclusterv1.HiveSecretsNotReadyMsg,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
		},
		{
			name: "ProwJob succeeded after deprovisioning, collect deprovisioning metric",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:00:00")),
					UID:               types.UID("test-ec-uid"),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					TearDownCluster: true,
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{Workflow: "e2e-aws"},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:30:00")),
					}},
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
					Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: EphemeralClusterTestName, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{"kubeconfig": []byte("kubeconfig")},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
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
			now: ptr.To(parseTime(t, "2025-04-02 12:45:00")),
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					CreationTimestamp: metav1.NewTime(parseTime(t, "2025-04-02 12:00:00")),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					TearDownCluster: true,
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{Workflow: "e2e-aws"},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioned,
					ProwJobID: "pj-123",
					SecretRef: "foo-credentials",
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:45:00")),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.CredentialsReadyReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:45:00")),
					}, {
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.TestDoneReason,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:45:00")),
					}, {
						Type:               ephemeralclusterv1.ProwJobCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.ProwJobCompletedReason,
						Message:            "prowjob state: success",
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:45:00")),
					}},
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Normal ClusterReady Cluster credentials are available",
				"Normal DeprovisioningStarted Deprovisioning signal sent",
				"Normal ProwJobSucceeded ProwJob pj-123 completed successfully",
			},
			wantProvisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{"e2e-aws"},
					SampleCount: 1,
					Buckets:     histogramBuckets(provisioningDurationBuckets, 0, 0, 0, 0, 1),
				},
			}},
			wantDeprovisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{"e2e-aws"},
					SampleCount: 1,
					Buckets:     histogramBuckets(deprovisioningDurationBuckets, 0, 0, 1),
				},
			}},
		},
		{
			name: "Hive cluster not ready yet, errs out when fetching a secret",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
					UID:       types.UID("test-ec-uid"),
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			objs: []ctrlclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
					Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{steps.LabelJobID: "pj-123"},
							Name:   "ci-op-1234",
						},
					},
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: api.HiveAdminPasswordSecret, Namespace: "ci-op-1234"},
						Data:       map[string][]byte{api.HiveAdminPasswordSecretKey: []byte("admin-passwd")},
					},
				}
				c := fake.NewClientBuilder().WithObjects(objs...).WithScheme(scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, client ctrlclient.WithWatch, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
							if _, ok := obj.(*corev1.Secret); ok && key.Name == HiveKubeconfigSecret {
								return errors.New("injected")
							}
							return client.Get(ctx, key, obj, opts...)
						},
					}).
					Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme, ctrlruntimetest.WithInitObjects(objs...)),
				}
			},
			wantEC: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "foo",
					Namespace:       "bar",
					ResourceVersion: "1000",
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{
							ClusterClaim: &api.ClusterClaim{},
						},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterProvisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.ProwJobCreating,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.ProwJobProperlyCreatedReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}, {
						Type:               ephemeralclusterv1.ClusterReady,
						Status:             metav1.ConditionFalse,
						Reason:             ephemeralclusterv1.SecretsFetchFailureReason,
						Message:            "read secret cluster-provisioning-hive-admin-kubeconfig/ci-op-1234: injected",
						LastTransitionTime: metav1.NewTime(fakeNow),
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
				WithStatusSubresource(tc.ec).
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

			provisioningDurationHistogramVec := newProvisioningDurationHistogramVec()
			deprovisioningDurationHistogramVec := newDeprovisioningDurationHistogramVec()
			metricsGatherer := newMetricsGatherer(logrus.NewEntry(logrus.StandardLogger()),
				nil, nil, provisioningDurationHistogramVec, deprovisioningDurationHistogramVec, "", 0)

			subTestFakeNow := fakeNow
			if tc.now != nil {
				subTestFakeNow = *tc.now
			}

			fakeEventRecorder := record.NewFakeRecorder(100)
			r := reconciler{
				logger:       logrus.NewEntry(logrus.StandardLogger()),
				masterClient: client,
				buildClients: clients,
				scheme:       scheme,
				now:          func() time.Time { return subTestFakeNow },
				polling:      func() time.Duration { return pollingTime },
				newProwJob:   newProwJobFaker("foobar", subTestFakeNow),
				recorder:     fakeEventRecorder,
				prowConfigAgent: prowConfigAgent(&prowconfig.Config{
					ProwConfig: prowconfig.ProwConfig{ProwJobNamespace: prowJobNamespace},
				}),
				collectProvisioningDurationMetricFunc:   metricsGatherer.collectProvisioningDuration,
				collectDeprovisioningDurationMetricFunc: metricsGatherer.collectDeprovisioningDuration,
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

			ignoreFields := cmpopts.IgnoreFields(ephemeralclusterv1.EphemeralCluster{}, "ResourceVersion", "UID")
			if diff := cmp.Diff(tc.wantEC, &gotEC, ignoreFields); diff != "" {
				t.Errorf("unexpected ephemeralcluster: %s", diff)
			}

			if tc.wantSecret != nil {
				gotSecret := corev1.Secret{}
				if err := client.Get(context.TODO(), types.NamespacedName{
					Name:      tc.wantSecret.Name,
					Namespace: tc.wantSecret.Namespace,
				}, &gotSecret); err != nil {
					t.Fatalf("get credentials secret: %s", err)
				}
				if diff := cmp.Diff(tc.wantSecret.Data, gotSecret.Data); diff != "" {
					t.Errorf("unexpected credentials secret data: %s", diff)
				}
				if !metav1.IsControlledBy(&gotSecret, tc.ec) {
					t.Errorf("credentials secret %s/%s is not controlled by ephemeralcluster %s",
						gotSecret.Namespace, gotSecret.Name, tc.ec.Name)
				}
			}

			pjList := prowv1.ProwJobList{}
			if err := client.List(context.TODO(), &pjList, &ctrlclient.ListOptions{}); err != nil {
				t.Fatalf("list prowjobs: %s", err.Error())
			}

			if len(pjList.Items) > 0 {
				testhelper.CompareWithFixture(t, pjList, testhelper.WithPrefix("pj_"))
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

			gotProvisioningDurationMetrics, err := collectHistogram(provisioningDurationHistogramVec.MetricVec)
			if err != nil {
				t.Fatalf("collect provision duration histogram: %s", err)
			}

			if diff := cmpMetrics(tc.wantProvisioningDurationHistogram, gotProvisioningDurationMetrics); diff != "" {
				t.Errorf("Provisioning duration metric differ: %s\n", diff)
			}

			gotDeprovisioningDurationMetrics, err := collectHistogram(deprovisioningDurationHistogramVec.MetricVec)
			if err != nil {
				t.Fatalf("collect deprovisioning duration histogram: %s", err)
			}

			if diff := cmpMetrics(tc.wantDeprovisioningDurationHistogram, gotDeprovisioningDurationMetrics); diff != "" {
				t.Errorf("Deprovisioning duration metric differ: %s\n", diff)
			}

			if diff := cmp.Diff(tc.wantEvents, drainEvents(fakeEventRecorder)); diff != "" {
				t.Errorf("events differ: %s\n", diff)
			}
		})
	}
}

func TestReconcileDeleteEphemeralCluster(t *testing.T) {
	scheme := fakeScheme(t)
	fakeNow := fakeNow(t)
	const pollingTime = 5

	for _, tc := range []struct {
		name                                string
		ec                                  *ephemeralclusterv1.EphemeralCluster
		pj                                  *prowv1.ProwJob
		buildClients                        func() map[string]*ctrlruntimetest.FakeClient
		wantEC                              *ephemeralclusterv1.EphemeralCluster
		wantPJ                              *prowv1.ProwJob
		wantSecret                          *corev1.Secret
		wantRes                             reconcile.Result
		wantDeprovisioningDurationHistogram []metric
		wantEvents                          []string
		wantErr                             error
	}{
		{
			name: "Start deprovision procedure",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				objs := []ctrlclient.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
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
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						Reason:             ephemeralclusterv1.TestDoneReason,
						LastTransitionTime: metav1.NewTime(fakeNow),
					}},
				},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
			},
			wantSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      api.EphemeralClusterTestDoneSignalSecretName,
					Namespace: "ci-op-1234",
				},
			},
			wantRes: reconcile.Result{RequeueAfter: pollingTime},
			wantEvents: []string{
				"Normal DeprovisioningStarted Deprovisioning signal sent",
			},
		},
		{
			name: "ProwJob is gone already, remove the finalizer and delete EC",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			wantRes: reconcile.Result{},
		},
		{
			name: "ci-operator NS not found: abort the PJ and remove the finalizer",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
				},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
			},
			buildClients: func() map[string]*ctrlruntimetest.FakeClient {
				c := fake.NewClientBuilder().WithScheme(scheme).Build()
				return map[string]*ctrlruntimetest.FakeClient{
					"build01": ctrlruntimetest.NewFakeClient(c, scheme),
				}
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{
					State:          prowv1.AbortedState,
					Description:    "EphemeralCluster being deleted: ci-operator NS not found",
					CompletionTime: ptr.To(metav1.NewTime(fakeNow)),
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning ProwJobAborted ProwJob pj-123 aborted: EphemeralCluster being deleted: ci-operator NS not found",
			},
		},
		{
			name: "Aborted ProwJob remove the finalizer and delete",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{ProwJobID: "pj-123"},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Status:     prowv1.ProwJobStatus{State: prowv1.AbortedState},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning ProwJobFailed ProwJob pj-123 finished with state: aborted",
			},
		},
		{
			name: "Build client not found: remove finalizer and abort PJ",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{ProwJobID: "pj-123"},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				Status:     prowv1.ProwJobStatus{State: prowv1.PendingState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				Status: prowv1.ProwJobStatus{
					State:          prowv1.AbortedState,
					CompletionTime: &metav1.Time{Time: fakeNow},
					Description:    "EphemeralCluster being deleted: build client not found for cluster build01",
				},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Warning ProwJobAborted ProwJob pj-123 aborted: EphemeralCluster being deleted: build client not found for cluster build01",
			},
		},
		{
			name: "ProwJob succeeded after deprovisioning, collect deprovisioning metric",
			ec: &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "foo",
					Namespace:         "bar",
					DeletionTimestamp: ptr.To(metav1.NewTime(fakeNow)),
					Finalizers:        []string{DependentProwJobFinalizer},
				},
				Spec: ephemeralclusterv1.EphemeralClusterSpec{
					CIOperator: ephemeralclusterv1.CIOperatorSpec{
						Test: ephemeralclusterv1.TestSpec{Workflow: "e2e-aws"},
					},
				},
				Status: ephemeralclusterv1.EphemeralClusterStatus{
					ProwJobID: "pj-123",
					Phase:     ephemeralclusterv1.EphemeralClusterDeprovisioning,
					Conditions: []metav1.Condition{{
						Type:               ephemeralclusterv1.TestCompleted,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(parseTime(t, "2025-04-02 12:00:00")),
					}},
				},
			},
			pj: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{Name: "pj-123", Namespace: prowJobNamespace},
				Spec:       prowv1.ProwJobSpec{Cluster: "build01"},
				Status:     prowv1.ProwJobStatus{State: prowv1.SuccessState},
			},
			wantRes: reconcile.Result{},
			wantEvents: []string{
				"Normal ProwJobSucceeded ProwJob pj-123 completed successfully",
			},
			wantDeprovisioningDurationHistogram: []metric{{
				Histogram: &histogram{
					Labels:      []string{"e2e-aws"},
					SampleCount: 1,
					Buckets:     histogramBuckets(deprovisioningDurationBuckets, 0, 0, 1),
				},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bldr := fake.NewClientBuilder().
				WithObjects(tc.ec).
				WithStatusSubresource(tc.ec).
				WithScheme(scheme)

			if tc.pj != nil {
				bldr = bldr.WithObjects(tc.pj)
			}

			client := bldr.Build()

			buildClients := make(map[string]ctrlclient.Client)
			if tc.buildClients != nil {
				fakeClients := tc.buildClients()
				for cluster, c := range fakeClients {
					buildClients[cluster] = c.WithWatch
				}
			}

			deprovisioningDurationHistogramVec := newDeprovisioningDurationHistogramVec()
			metricsGatherer := newMetricsGatherer(logrus.NewEntry(logrus.StandardLogger()),
				nil, nil, nil, deprovisioningDurationHistogramVec, "", 0)

			fakeEventRecorder := record.NewFakeRecorder(100)
			r := reconciler{
				logger:       logrus.NewEntry(logrus.StandardLogger()),
				masterClient: client,
				buildClients: buildClients,
				now:          func() time.Time { return fakeNow },
				polling:      func() time.Duration { return pollingTime },
				recorder:     fakeEventRecorder,
				prowConfigAgent: prowConfigAgent(&prowconfig.Config{
					ProwConfig: prowconfig.ProwConfig{ProwJobNamespace: prowJobNamespace},
				}),
				collectDeprovisioningDurationMetricFunc: metricsGatherer.collectDeprovisioningDuration,
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

				ignoreFields := cmpopts.IgnoreFields(ephemeralclusterv1.EphemeralCluster{}, "ResourceVersion", "UID")
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

			if tc.wantSecret != nil {
				buildClient := buildClients[tc.pj.Spec.Cluster]
				gotSecret := corev1.Secret{}
				if err := buildClient.Get(context.TODO(), types.NamespacedName{
					Name:      tc.wantSecret.Name,
					Namespace: tc.wantSecret.Namespace,
				}, &gotSecret); err != nil {
					t.Fatalf("get credentials secret: %s", err)
				}
				if diff := cmp.Diff(tc.wantSecret.Data, gotSecret.Data); diff != "" {
					t.Errorf("unexpected credentials secret data: %s", diff)
				}
			}

			gotDeprovisioningDurationMetrics, err := collectHistogram(deprovisioningDurationHistogramVec.MetricVec)
			if err != nil {
				t.Fatalf("collect deprovisioning duration histogram: %s", err)
			}

			if diff := cmpMetrics(tc.wantDeprovisioningDurationHistogram, gotDeprovisioningDurationMetrics); diff != "" {
				t.Errorf("Deprovisioning duration metric differ: %s\n", diff)
			}

			if diff := cmp.Diff(tc.wantEvents, drainEvents(fakeEventRecorder)); diff != "" {
				t.Errorf("events differ: %s\n", diff)
			}
		})
	}
}
