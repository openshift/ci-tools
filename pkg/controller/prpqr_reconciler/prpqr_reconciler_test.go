package prpqr_reconciler

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/ci-tools/pkg/api"
	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

func TestReconcile(t *testing.T) {
	testCases := []struct {
		name     string
		prpqr    *v1.PullRequestPayloadQualificationRun
		expected *v1.PullRequestPayloadQualificationRun
	}{
		{
			name: "basic case",
			prpqr: &v1.PullRequestPayloadQualificationRun{
				ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
				Spec: v1.PullRequestPayloadTestSpec{
					PullRequest: v1.PullRequestUnderTest{
						Org:         "droslean",
						Repo:        "test",
						BaseRef:     "12345",
						BaseSHA:     "123456",
						PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
					Jobs: v1.PullRequestPayloadJobSpec{
						ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
						Jobs: []v1.ReleaseJobSpec{
							{
								CIOperatorConfig: api.Metadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"},
								Test:             "test-name"},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		r := &reconciler{
			logger: logrus.WithField("test-name", tc.name),
			client: fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.prpqr).Build(),
		}
		req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "test-namespace", Name: "prpqr-test"}}
		if err := r.reconcile(context.Background(), req, r.logger); err != nil {
			t.Fatal(err)
		}
		expectedProwjobs := generateProwjobs(tc.prpqr.Spec.PullRequest.Org, tc.prpqr.Spec.PullRequest.Repo, tc.prpqr.Spec.PullRequest.BaseRef, tc.prpqr.Name, tc.prpqr.Namespace)
		var actualProwjobs prowv1.ProwJobList
		if err := r.client.List(context.Background(), &actualProwjobs); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(actualProwjobs.Items, expectedProwjobs, cmpopts.IgnoreFields(prowv1.ProwJob{}, "ResourceVersion", "Status.StartTime")); diff != "" {
			t.Fatal(diff)
		}
	}
}
