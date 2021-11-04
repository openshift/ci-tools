package pjstatussyncer

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

func TestReconcile(t *testing.T) {
	testCases := []struct {
		name        string
		prowjobs    []ctrlruntimeclient.Object
		pjMutations func(pj ctrlruntimeclient.Object, client ctrlruntimeclient.Client, t *testing.T)
		prpqr       []ctrlruntimeclient.Object
		expected    []v1.PullRequestPayloadQualificationRun
	}{
		{
			name: "basic case",
			prowjobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "test-pj", Namespace: "test-namespace", Labels: map[string]string{"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test"}},
					Status:     prowv1.ProwJobStatus{StartTime: metav1.Time{Time: time.Now()}},
				},
			},
			pjMutations: func(obj ctrlruntimeclient.Object, client ctrlruntimeclient.Client, t *testing.T) {
				pj, _ := obj.(*prowv1.ProwJob)
				pj.Status.State = prowv1.SuccessState

				if err := client.Update(context.Background(), pj); err != nil {
					t.Fatal(err)
				}
			},
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Status: v1.PullRequestPayloadTestStatus{
						Jobs: []v1.PullRequestPayloadJobStatus{
							{
								ReleaseJobName: "release-job-name",
								ProwJob:        "test-pj",
								Status:         prowv1.ProwJobStatus{State: prowv1.TriggeredState},
							},
						},
					},
				},
			},

			expected: []v1.PullRequestPayloadQualificationRun{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Status: v1.PullRequestPayloadTestStatus{
						Jobs: []v1.PullRequestPayloadJobStatus{
							{
								ReleaseJobName: "release-job-name",
								ProwJob:        "test-pj",
								Status:         prowv1.ProwJobStatus{State: prowv1.SuccessState},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		r := &reconciler{
			logger: logrus.WithField("test-name", tc.name),
			client: fakectrlruntimeclient.NewClientBuilder().WithObjects(append(tc.prowjobs, tc.prpqr...)...).Build(),
		}

		for _, pj := range tc.prowjobs {
			if tc.pjMutations != nil {
				tc.pjMutations(pj, r.client, t)
			}

			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: pj.GetNamespace(), Name: pj.GetName()}}
			if err := r.reconcile(context.Background(), r.logger, req); err != nil {
				t.Fatal(err)
			}
		}

		var actualPrpqr v1.PullRequestPayloadQualificationRunList
		if err := r.client.List(context.Background(), &actualPrpqr); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(actualPrpqr.Items, tc.expected, cmpopts.IgnoreFields(prowv1.ProwJobStatus{}, "StartTime"), cmpopts.IgnoreFields(v1.PullRequestPayloadQualificationRun{}, "TypeMeta.Kind", "TypeMeta.APIVersion", "ResourceVersion")); diff != "" {
			t.Fatal(diff)
		}
	}
}
