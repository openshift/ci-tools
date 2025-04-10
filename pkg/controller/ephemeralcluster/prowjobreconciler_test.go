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

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
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
			obj:        &pjObjectMock{labels: map[string]string{EphemeralClusterProwJobLabel: ""}},
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
	fakeNow := fakeNow(t)
	scheme := fakeScheme(t)

	for _, tc := range []struct {
		name         string
		pj           *prowv1.ProwJob
		ec           *ephemeralclusterv1.EphemeralCluster
		interceptors interceptor.Funcs
		wantEC       *ephemeralclusterv1.EphemeralCluster
		wantPJ       *prowv1.ProwJob
		wantRes      reconcile.Result
		wantErr      error
	}{
		{
			name: "No EphemeralCluster label, stop reconciling",
			ec:   &ephemeralclusterv1.EphemeralCluster{},
			pj: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{Name: "foo", Namespace: "bar"},
			},
			wantEC:  &ephemeralclusterv1.EphemeralCluster{},
			wantRes: reconcile.Result{},
			wantErr: reconcile.TerminalError(errors.New("foo doesn't have the EC label")),
		},
		{
			name: "EphemeralCluster not found, aborting the ProwJob",
			pj: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterProwJobLabel: "",
						EphemeralClusterNameLabel:    "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
			},
			wantPJ: &prowv1.ProwJob{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						EphemeralClusterProwJobLabel: "",
						EphemeralClusterNameLabel:    "ec",
					},
					Name:      "foo",
					Namespace: "bar",
				},
				Status: prowv1.ProwJobStatus{
					State:          prowv1.AbortedState,
					Description:    AbortECNotFound,
					CompletionTime: ptr.To(v1.NewTime(fakeNow)),
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bldr := fake.NewClientBuilder().
				WithInterceptorFuncs(tc.interceptors).
				WithScheme(scheme)
			client := addObjs(bldr, tc.pj, tc.ec).Build()

			r := prowJobReconciler{
				logger: logrus.NewEntry(logrus.StandardLogger()),
				client: client,
				now:    func() time.Time { return fakeNow },
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
		})
	}
}
