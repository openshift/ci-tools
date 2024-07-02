package prpqr_reconciler

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	v1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestReconcile(t *testing.T) {

	logrus.SetLevel(logrus.DebugLevel)
	testCases := []struct {
		name       string
		prowJobs   []ctrlruntimeclient.Object
		prpqr      []ctrlruntimeclient.Object
		prowConfig prowconfig.Config
	}{
		{
			name: "basic case",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "basic case with scheduling",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
			prowConfig: prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Scheduler: prowconfig.Scheduler{Enabled: true}}},
		},
		{
			name: "basic case without PR; testing specified base",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456"}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "basic case with vsphere override",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-vsphere"}},
						},
					},
				},
			},
		},
		{
			name: "basic case with metal override",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-metal"}},
						},
					},
				},
			},
		},
		{
			name: "basic case with variant",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch", Variant: "test-variant"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "basic case, prowjob already exists, no updates",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}}},
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
		},
		{
			name: "multiple PRs from different repositories",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{
							{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
							{Org: "test-org", Repo: "another-test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 101, Author: "test", SHA: "123452", Title: "test-pr"}},
						},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "multiple PRs from the same repository",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{
							{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}},
							{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 101, Author: "test", SHA: "123452", Title: "test-pr"}},
						},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "multiple case, one of the prowjobs already exists",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
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
		},
		{
			name: "basic aggregated case",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name", AggregatedCount: 2}},
						},
					},
				},
			},
		},
		{
			name: "basic aggregated case with scheduling",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name", AggregatedCount: 2}},
						},
					},
				},
			},
			prowConfig: prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Scheduler: prowconfig.Scheduler{Enabled: true}}},
		},
		{
			name: "override initial and base payload pullspecs",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
						InitialPayloadBase: "quay.io/openshift-release-dev/ocp-release:4.15.12-x86_64",
						PayloadOverrides:   v1.PayloadOverrides{BasePullSpec: "quay.io/openshift-release-dev/ocp-release:4.16.0-ec.1-x86_64"},
					},
				},
			},
		},
		{
			name: "all jobs are aborted remove dependant prowjobs finalizer",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace", Finalizers: []string{dependentProwJobsFinalizer}},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
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
					Status: prowv1.ProwJobStatus{State: "aborted"},
				},
			},
		},
		{
			name: "delete when all jobs are done",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "prpqr-test",
						Namespace:         "test-namespace",
						DeletionTimestamp: &zeroTime,
						Finalizers:        []string{dependentProwJobsFinalizer},
					},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs: []v1.ReleaseJobSpec{
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-1"},
								{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name-2"},
							},
						},
					},

					Status: v1.PullRequestPayloadTestStatus{
						Jobs: []v1.PullRequestPayloadJobStatus{
							{
								ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name-1",
								ProwJob:        "uuid-1",
								Status:         prowv1.ProwJobStatus{StartTime: zeroTime, State: prowv1.AbortedState},
							},
							{
								ReleaseJobName: "periodic-ci-test-org-test-repo-test-branch-test-name-2",
								ProwJob:        "uuid-2",
								Status:         prowv1.ProwJobStatus{StartTime: zeroTime, State: prowv1.SuccessState},
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
						Name:      "uuid-1",
						Annotations: map[string]string{
							"prow.k8s.io/context": "",
							"prow.k8s.io/job":     "",
							"releaseJobName":      "periodic-ci-test-org-test-repo-test-branch-test-name-1",
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
							"releaseJobNameHash": "283d99c0dcbd80070e6816420ac68caa",
						},
					},
					Status: prowv1.ProwJobStatus{State: prowv1.AbortedState},
				},
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-namespace",
						Name:      "uuid-2",
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
					Status: prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &reconciler{
				logger:               logrus.WithField("test-name", tc.name),
				client:               fakectrlruntimeclient.NewClientBuilder().WithObjects(append(tc.prpqr, tc.prowJobs...)...).Build(),
				configResolverClient: &fakeResolverClient{},
				prowConfigGetter:     &fakeProwConfigGetter{cfg: &tc.prowConfig},
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "test-namespace", Name: "prpqr-test"}}
			if err := r.reconcile(context.Background(), req, r.logger); err != nil {
				t.Fatal(err)
			}

			var actualProwjobsList prowv1.ProwJobList
			if err := r.client.List(context.Background(), &actualProwjobsList); err != nil {
				t.Fatal(err)
			}

			pruneProwjobsForTests(t, actualProwjobsList.Items)
			sort.Slice(actualProwjobsList.Items, func(i, j int) bool {
				return actualProwjobsList.Items[i].Labels["releaseJobNameHash"] < actualProwjobsList.Items[j].Labels["releaseJobNameHash"]
			})

			testhelper.CompareWithFixture(t, actualProwjobsList.Items, testhelper.WithPrefix("prowjobs-"))

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
			items[i].Status.Jobs[job].ProwJob = "some-uuid"
			items[i].Status.Jobs[job].Status.StartTime = zeroTime

		}
		for condition := range items[i].Status.Conditions {
			items[i].Status.Conditions[condition].LastTransitionTime = zeroTime
		}
	}
}

func pruneProwjobsForTests(t *testing.T, items []prowv1.ProwJob) {
	for i, pj := range items {
		if strings.HasPrefix(pj.Spec.Job, "aggregator") {
			unResolvedConfig := items[i].Spec.PodSpec.Containers[0].Env[0].Value

			c := &api.ReleaseBuildConfiguration{}
			if err := yaml.Unmarshal([]byte(unResolvedConfig), c); err != nil {
				t.Fatal(err)
			}

			if _, ok := c.Tests[0].MultiStageTestConfiguration.Environment["JOB_START_TIME"]; ok {
				c.Tests[0].MultiStageTestConfiguration.Environment["JOB_START_TIME"] = "1970-01-01T01:00:00+01:00"
			}

			unresolvedConfigRaw, err := yaml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}

			items[i].Spec.PodSpec.Containers[0].Env[0].Value = string(unresolvedConfigRaw)
		}

		items[i].Status.StartTime = zeroTime
		items[i].Name = "some-uuid"
	}
}

type fakeResolverClient struct{}

func (f *fakeResolverClient) ConfigWithTest(base *api.Metadata, testSource *api.MetadataWithTest) (*api.ReleaseBuildConfiguration, error) {
	return &api.ReleaseBuildConfiguration{
		Metadata: *base,
		Tests: []api.TestStepConfiguration{
			{
				As: testSource.Test,
			},
		},
	}, nil
}

type fakeProwConfigGetter struct {
	cfg *prowconfig.Config
}

func (f *fakeProwConfigGetter) Defaulter() periodicDefaulter {
	return &fakePeriodicDefaulter{}
}

func (f *fakeProwConfigGetter) Config() *prowconfig.Config {
	return f.cfg
}

type fakePeriodicDefaulter struct{}

func (f *fakePeriodicDefaulter) DefaultPeriodic(periodic *prowconfig.Periodic) error {
	// The current default cluster is hardcoded to "build01".  If there is a different value, then
	// the underlying logic purposefully overwrote the default.  There are currently 2 tests to verify
	// the existing overrides.
	if periodic.Cluster != "build01" {
		periodic.Cluster = "cluster-name-overwritten"
	} else {
		periodic.Cluster = "cluster-name-defaulted"
	}
	return nil
}
