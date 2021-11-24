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
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

func TestReconcile(t *testing.T) {
	testCases := []struct {
		name             string
		prowJobs         []ctrlruntimeclient.Object
		expectedProwjobs []prowv1.ProwJob
		prpqr            []ctrlruntimeclient.Object
		expected         []v1.PullRequestPayloadQualificationRun
	}{
		{
			name: "basic case",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
			expected: []v1.PullRequestPayloadQualificationRun{
				{
					TypeMeta:   metav1.TypeMeta{Kind: "PullRequestPayloadQualificationRun", APIVersion: "ci.openshift.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}}},
					},
					Status: v1.PullRequestPayloadTestStatus{
						Conditions: []metav1.Condition{
							{
								Status:  "True",
								Reason:  "AllJobsTriggered",
								Message: "All jobs triggered successfully",
							},
						},

						Jobs: []v1.PullRequestPayloadJobStatus{{ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name", Status: prowv1.ProwJobStatus{State: "triggered"}}},
					},
				},
			},
			expectedProwjobs: []prowv1.ProwJob{
				{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"prow.k8s.io/context": "",
							"prow.k8s.io/job":     "",
							"releaseJobName":      "periodic-ci-test-org-test-repo-test-branch-test-name",
						},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "ee3858eff62263cd7266320c00d1d38b",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
		},
		{
			name: "basic case, prowjob already exists, no updates",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}}},
					},
				},
			},
			expected: []v1.PullRequestPayloadQualificationRun{
				{
					TypeMeta:   metav1.TypeMeta{Kind: "PullRequestPayloadQualificationRun", APIVersion: "ci.openshift.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
					Status: v1.PullRequestPayloadTestStatus{
						Conditions: []metav1.Condition{
							{
								Status:  "True",
								Reason:  "AllJobsTriggered",
								Message: "All jobs triggered successfully",
							},
						},
					},
				},
			},
			prowJobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"prow.k8s.io/context": "",
							"prow.k8s.io/job":     "",
							"releaseJobName":      "periodic-ci-test-org-test-repo-test-branch-test-name",
						},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "ee3858eff62263cd7266320c00d1d38b",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
			expectedProwjobs: []prowv1.ProwJob{
				{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"prow.k8s.io/context": "",
							"prow.k8s.io/job":     "",
							"releaseJobName":      "periodic-ci-test-org-test-repo-test-branch-test-name",
						},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "ee3858eff62263cd7266320c00d1d38b",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
		},
		{
			name: "multiple case, one of the prowjobs already exists",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs: []v1.ReleaseJobSpec{
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"},
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-2"},
							},
						},
					},
					Status: v1.PullRequestPayloadTestStatus{
						Jobs: []v1.PullRequestPayloadJobStatus{{ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name", Status: prowv1.ProwJobStatus{State: "triggered"}}}},
				},
			},
			expected: []v1.PullRequestPayloadQualificationRun{
				{
					TypeMeta:   metav1.TypeMeta{Kind: "PullRequestPayloadQualificationRun", APIVersion: "ci.openshift.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequest: v1.PullRequestUnderTest{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs: []v1.ReleaseJobSpec{
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"},
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-2"},
							},
						},
					},
					Status: v1.PullRequestPayloadTestStatus{
						Conditions: []metav1.Condition{
							{
								Status:  "True",
								Reason:  "AllJobsTriggered",
								Message: "All jobs triggered successfully",
							},
						},
						Jobs: []v1.PullRequestPayloadJobStatus{
							{ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name", Status: prowv1.ProwJobStatus{State: "triggered"}},
							{ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name-2", Status: prowv1.ProwJobStatus{State: "triggered"}},
						},
					},
				},
			},
			prowJobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "test-namespace",
						Annotations: map[string]string{"prow.k8s.io/context": "", "prow.k8s.io/job": ""},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "ee3858eff62263cd7266320c00d1d38b",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
			expectedProwjobs: []prowv1.ProwJob{
				{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "test-namespace",
						Annotations: map[string]string{"prow.k8s.io/context": "", "prow.k8s.io/job": ""},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "ee3858eff62263cd7266320c00d1d38b",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
				{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"prow.k8s.io/context": "",
							"prow.k8s.io/job":     "",
							"releaseJobName":      "periodic-ci-test-org-test-repo-test-branch-test-name-2",
						},
						Labels: map[string]string{
							"created-by-prow":           "true",
							"prow.k8s.io/context":       "",
							"prow.k8s.io/job":           "",
							"prow.k8s.io/refs.base_ref": "test-branch",
							"prow.k8s.io/refs.org":      "test-org",
							"prow.k8s.io/refs.repo":     "test-repo",
							"prow.k8s.io/type":          "periodic",
							"pullrequestpayloadqualificationruns.ci.openshift.io": "prpqr-test",
							"releaseJobNameHash": "428af2aff595f9d8074f2f6bfba1aec1",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &reconciler{
				logger: logrus.WithField("test-name", tc.name),
				client: fakectrlruntimeclient.NewClientBuilder().WithObjects(append(tc.prpqr, tc.prowJobs...)...).Build(),
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "test-namespace", Name: "prpqr-test"}}
			if err := r.reconcile(context.Background(), req, r.logger); err != nil {
				t.Fatal(err)
			}

			var actualProwjobsList prowv1.ProwJobList
			if err := r.client.List(context.Background(), &actualProwjobsList); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(actualProwjobsList.Items, tc.expectedProwjobs, cmpopts.IgnoreFields(prowv1.ProwJob{}, "Spec", "ResourceVersion", "Status.StartTime", "ObjectMeta.Name")); diff != "" {
				t.Fatal(diff)
			}

			var actualPrpqr v1.PullRequestPayloadQualificationRunList
			if err := r.client.List(context.Background(), &actualPrpqr); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(actualPrpqr.Items, tc.expected,
				cmpopts.IgnoreFields(metav1.TypeMeta{}, "Kind", "APIVersion"),
				cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
				cmpopts.IgnoreFields(prowv1.ProwJobStatus{}, "StartTime"),
				cmpopts.IgnoreFields(v1.PullRequestPayloadQualificationRun{}, "ResourceVersion"),
				cmpopts.IgnoreFields(v1.PullRequestPayloadJobStatus{}, "ProwJob")); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
