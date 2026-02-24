package prpqr_reconciler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		name          string
		prowJobs      []ctrlruntimeclient.Object
		prpqr         []ctrlruntimeclient.Object
		prowConfig    prowconfig.Config
		omitStatusURL bool
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
			name: "basic case with no PRs included; testing determined base",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
					},
				},
			},
		},
		{
			name: "basic case where test name is not found",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main"}, Test: "missing"}},
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
							"releaseJobNameHash": "bff80ea4af62f87fcac06a79fc7b242f6f07932f08cdba39ebd7e808",
						},
					},
					Status: prowv1.ProwJobStatus{State: "triggered"},
				},
			},
		},
		{
			name: "basic case with sharded job",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name", ShardCount: 3, ShardIndex: 1}},
						},
					},
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
							"releaseJobNameHash": "bff80ea4af62f87fcac06a79fc7b242f6f07932f08cdba39ebd7e808",
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
			name: "override tag",
			prpqr: []ctrlruntimeclient.Object{
				&v1.PullRequestPayloadQualificationRun{
					ObjectMeta: metav1.ObjectMeta{Name: "prpqr-test", Namespace: "test-namespace"},
					Spec: v1.PullRequestPayloadTestSpec{
						PullRequests: []v1.PullRequestUnderTest{{Org: "test-org", Repo: "test-repo", BaseRef: "test-branch", BaseSHA: "123456", PullRequest: &v1.PullRequest{Number: 100, Author: "test", SHA: "12345", Title: "test-pr"}}},
						Jobs: v1.PullRequestPayloadJobSpec{
							ReleaseControllerConfig: v1.ReleaseControllerConfig{OCP: "4.9", Release: "ci", Specifier: "informing"},
							Jobs:                    []v1.ReleaseJobSpec{{CIOperatorConfig: v1.CIOperatorMetadata{Org: "test-org", Repo: "test-repo", Branch: "test-branch"}, Test: "test-name"}},
						},
						PayloadOverrides: v1.PayloadOverrides{
							ImageTagOverrides: []v1.ImageTagOverride{
								{
									Name:  "machine-os-content",
									Image: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:9a49368aad56c984302c3cfd7d3dfd3186687381ca9a94501960b0d6a8fb7f98",
								},
							},
						},
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
							"releaseJobNameHash": "bff80ea4af62f87fcac06a79fc7b242f6f07932f08cdba39ebd7e808",
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
							"releaseJobNameHash": "82f08539662804d4d991e8039d995c52aea2ecdb202482a807a8f0a9",
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
							"releaseJobNameHash": "fca4edde38266d4bc96d149e6160540c9f748c2fbf5b4cfd6f07a785",
						},
					},
					Status: prowv1.ProwJobStatus{State: prowv1.SuccessState},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			createInterceptor := func(omitStatusURL bool) func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
				return func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
					if prowJob, ok := obj.(*prowv1.ProwJob); ok && !omitStatusURL {
						prowJob.Status.URL = fmt.Sprintf("https://prow.ci.openshift.org/view/gs/test-platform-results/%s", prowJob.Spec.Job)
					}
					return client.Create(ctx, obj, opts...)
				}
			}
			client := fakectrlruntimeclient.NewClientBuilder().
				WithObjects(append(tc.prpqr, tc.prowJobs...)...).
				WithInterceptorFuncs(
					interceptor.Funcs{
						Create: createInterceptor(tc.omitStatusURL),
					}).
				Build()
			r := &reconciler{
				logger:                      logrus.WithField("test-name", tc.name),
				client:                      client,
				configResolverClient:        &fakeResolverClient{},
				prowConfigGetter:            &fakeProwConfigGetter{cfg: &tc.prowConfig},
				dispatcherClient:            &fakeDispatcherClient{},
				jobTriggerWaitDuration:      time.Duration(1) * time.Second,
				defaultAggregatorJobTimeout: time.Duration(6) * time.Hour,
				defaultMultiRefJobTimeout:   time.Duration(6) * time.Hour,
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "test-namespace", Name: "prpqr-test"}}
			if err := r.reconcile(context.Background(), req, r.logger, time.Millisecond); err != nil {
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
	findUnresolvedConfigEnv := func(envs []corev1.EnvVar) *corev1.EnvVar {
		for i := range envs {
			if e := &envs[i]; e.Name == "UNRESOLVED_CONFIG" {
				return e
			}
		}
		return nil
	}

	for i, pj := range items {
		if strings.HasPrefix(pj.Spec.Job, "aggregator") {
			unresolvedConfigEnv := findUnresolvedConfigEnv(items[i].Spec.PodSpec.Containers[0].Env)
			if unresolvedConfigEnv == nil {
				t.Errorf("UNRESOLVED_CONFIG not set on prowjob %s", pj.Spec.Job)
				continue
			}

			unresolvedConfig := unresolvedConfigEnv.Value

			c := &api.ReleaseBuildConfiguration{}
			if err := yaml.Unmarshal([]byte(unresolvedConfig), c); err != nil {
				t.Fatal(err)
			}

			if _, ok := c.Tests[0].MultiStageTestConfiguration.Environment["JOB_START_TIME"]; ok {
				c.Tests[0].MultiStageTestConfiguration.Environment["JOB_START_TIME"] = "1970-01-01T01:00:00+01:00"
			}

			unresolvedConfigRaw, err := yaml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}

			unresolvedConfigEnv.Value = string(unresolvedConfigRaw)
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

func (f *fakePeriodicDefaulter) DefaultPeriodic(_ *prowconfig.Periodic) error {
	return nil
}

type fakeDispatcherClient struct{}

func (f *fakeDispatcherClient) ClusterForJob(jobName string) (string, error) {
	if jobName == "periodic-ci-openshift-release-main-missing" {
		return "", fmt.Errorf("job: %s not found", jobName)
	} else {
		return "build02", nil
	}
}
