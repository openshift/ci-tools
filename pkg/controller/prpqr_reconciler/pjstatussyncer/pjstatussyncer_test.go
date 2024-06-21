package pjstatussyncer

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/testhelper"
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
					Status:     prowv1.ProwJobStatus{State: prowv1.TriggeredState, StartTime: metav1.Time{Time: time.Now()}},
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
		},
		{
			name: "previous condition exists",
			prowjobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: "test-pj", Namespace: "test-namespace", Labels: map[string]string{"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test"}},
					Status:     prowv1.ProwJobStatus{State: prowv1.TriggeredState, StartTime: metav1.Time{Time: time.Now()}},
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
						Conditions: []metav1.Condition{
							{
								Type:    "AllJobsFinished",
								Status:  "False",
								Reason:  "AllJobsFinished",
								Message: "jobs [test-pj] still running",
							},
						},
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
		},
		{
			name: "job is still running",
			prowjobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{UID: "1", Name: "test-pj-running", Namespace: "test-namespace", Labels: map[string]string{"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test"}},
					Status:     prowv1.ProwJobStatus{State: prowv1.TriggeredState, StartTime: metav1.Time{Time: time.Now()}},
				},
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{UID: "2", Name: "test-pj", Namespace: "test-namespace", Labels: map[string]string{"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test"}},
					Status:     prowv1.ProwJobStatus{State: prowv1.TriggeredState, StartTime: metav1.Time{Time: time.Now()}},
				},
			},
			pjMutations: func(obj ctrlruntimeclient.Object, client ctrlruntimeclient.Client, t *testing.T) {
				pj, _ := obj.(*prowv1.ProwJob)
				pj.Status.State = prowv1.SuccessState
				if pj.Name == "test-pj-running" {
					pj.Status.State = prowv1.PendingState
				}
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
							{
								ReleaseJobName: "release-job-name-2",
								ProwJob:        "test-pj-running",
								Status:         prowv1.ProwJobStatus{State: prowv1.TriggeredState},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

			prunePRPQRForTests(actualPrpqr.Items)
			testhelper.CompareWithFixture(t, actualPrpqr.Items, testhelper.WithPrefix("prpqr-"))
		})
	}
}

var (
	zeroTime = metav1.NewTime(time.Unix(0, 0))
)

func prunePRPQRForTests(items []v1.PullRequestPayloadQualificationRun) {
	for i := range items {
		for job := range items[i].Status.Jobs {
			items[i].ObjectMeta.ResourceVersion = ""
			items[i].Status.Jobs[job].Status.StartTime = zeroTime
		}
		for condition := range items[i].Status.Conditions {
			items[i].Status.Conditions[condition].LastTransitionTime = zeroTime
		}
	}
}
