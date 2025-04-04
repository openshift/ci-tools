package ephemeralcluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pjutil"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
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

func TestCreateProwJob(t *testing.T) {
	fakeNow, err := time.Parse("2006-01-02 15:04:05", "2025-04-02 12:12:12")
	if err != nil {
		t.Fatalf("parse fake now: %s", err)
	}

	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(ephemeralclusterv1.AddToScheme, prowv1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatal("build scheme")
	}

	for _, tc := range []struct {
		name            string
		ec              ephemeralclusterv1.EphemeralCluster
		req             reconcile.Request
		interceptors    interceptor.Funcs
		configUploadErr error
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
			req: reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
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
			req: reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
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
			req: reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "ec"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithObjects(&tc.ec).
				WithScheme(scheme).
				WithInterceptorFuncs(tc.interceptors).
				Build()

			r := reconciler{
				logger:         logrus.NewEntry(logrus.StandardLogger()),
				masterClient:   client,
				now:            func() time.Time { return fakeNow },
				newPresubmit:   newPresubmitFaker("foobar", fakeNow),
				configUploader: &fakeGCSUploader{err: tc.configUploadErr},
			}

			_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: tc.ec.Namespace, Name: tc.ec.Name}})
			if err != nil {
				t.Errorf("unexpected reconcile error: %s", err)
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
