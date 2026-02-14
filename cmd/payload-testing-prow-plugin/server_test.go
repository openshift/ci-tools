package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/github/fakegithub"
	"sigs.k8s.io/prow/pkg/kube"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func (j1 jobSetSpecification) Equals(j2 jobSetSpecification) bool {
	return cmp.Equal(j1, j2, cmp.AllowUnexported(jobSetSpecification{}))
}

func TestSpecsFromComment(t *testing.T) {
	testCases := []struct {
		name     string
		comment  string
		expected []jobSetSpecification
	}{
		{
			name:     "single job type",
			comment:  "/payload 4.10 nightly informing",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}},
		},
		{
			name:     "all",
			comment:  "/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.8", releaseType: "ci", jobs: "all"}},
		},
		{
			name:    "unknown command",
			comment: "/cmd 4.8 ci all",
		},
		{
			name:     "multiple match",
			comment:  "/payload 4.10 nightly informing\n/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}, {ocp: "4.8", releaseType: "ci", jobs: "all"}},
		},
		{
			name:     "includes additional PR",
			comment:  "/payload-with-prs 4.10 nightly informing openshift/kubernetes#1234",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing", additionalPRs: []config.AdditionalPR{"openshift/kubernetes#1234"}}},
		},
		{
			name:     "all includes multiple PRs",
			comment:  "/payload-with-prs 4.10 ci all openshift/kubernetes#1234 openshift/installer#999",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "ci", jobs: "all", additionalPRs: []config.AdditionalPR{"openshift/kubernetes#1234", "openshift/installer#999"}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := specsFromComment(tc.comment)
			if diff := cmp.Diff(tc.expected, actual, cmp.Comparer(func(x, y jobSetSpecification) bool {
				return cmp.Diff(x.ocp, y.ocp) == "" && cmp.Diff(x.releaseType, y.releaseType) == "" && cmp.Diff(x.jobs, y.jobs) == ""
			})); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestJobNamesFromComment(t *testing.T) {
	testCases := []struct {
		name     string
		comment  string
		expected []config.Job
	}{
		{
			name:    "no job name",
			comment: "/payload-job",
		},
		{
			name:     "single job",
			comment:  "/payload-job periodic-ci-openshift-release-some-job",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}},
		},
		{
			name:     "multiple jobs",
			comment:  "/payload-job periodic-ci-openshift-release-some-job periodic-ci-openshift-release-another-job",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}, {Name: "periodic-ci-openshift-release-another-job"}},
		},
		{
			name:     "multiple match",
			comment:  "/payload-job periodic-ci-openshift-release-some-job periodic-ci-openshift-release-another-job\n/cmd 4.8 ci all\n/payload-job periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}, {Name: "periodic-ci-openshift-release-another-job"}, {Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"}},
		},
		{
			name:     "job with additional PR",
			comment:  "/payload-job-with-prs periodic-ci-openshift-release-some-job openshift/installer#123",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", WithPRs: []config.AdditionalPR{"openshift/installer#123"}}},
		},
		{
			name:     "job with multiple additional PRs",
			comment:  "/payload-job-with-prs periodic-ci-openshift-release-some-job openshift/installer#123 openshift/kubernetes#1234",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", WithPRs: []config.AdditionalPR{"openshift/installer#123", "openshift/kubernetes#1234"}}},
		},
		{
			name:     "/payload-aggregate periodic-ci-openshift-release-some-job   10  ",
			comment:  "/payload-aggregate periodic-ci-openshift-release-some-job   10  ",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", AggregatedCount: 10}},
		},
		{
			name:     "payload aggregate with additional PR",
			comment:  "/payload-aggregate-with-prs periodic-ci-openshift-release-some-job 10 openshift/installer#123",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", AggregatedCount: 10, WithPRs: []config.AdditionalPR{"openshift/installer#123"}}},
		},
		{
			name:     "payload aggregate with multiple additional PRs",
			comment:  "/payload-aggregate-with-prs periodic-ci-openshift-release-some-job 10 openshift/installer#123 openshift/kubernetes#1234",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", AggregatedCount: 10, WithPRs: []config.AdditionalPR{"openshift/installer#123", "openshift/kubernetes#1234"}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := jobsFromComment(tc.comment)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestMessage(t *testing.T) {
	testCases := []struct {
		name     string
		spec     jobSetSpecification
		expected string
	}{
		{
			name: "basic case",
			spec: jobSetSpecification{ocp: "4.10", releaseType: "nightly", jobs: "informing"},
			expected: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
- dummy-ocp-4.10-nightly-informing-job1
- dummy-ocp-4.10-nightly-informing-job2
`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := message(tc.spec, fakeResolve(tc.spec.ocp, tc.spec.releaseType, tc.spec.jobs))
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestGetPayloadJobsForPR(t *testing.T) {
	testCases := []struct {
		name     string
		org      string
		repo     string
		prNumber int
		s        *server
		expected []string
	}{
		{
			name:     "jobs exist in the proper states",
			org:      "org",
			repo:     "repo",
			prNumber: 123,
			s: &server{
				kubeClient: fakeclient.NewClientBuilder().WithRuntimeObjects(
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Name:      "1",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "123",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "some-job",
								},
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.TriggeredState},
									ProwJob: "another-job",
								},
							},
						},
					},
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Name:      "2",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "123",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "different-job",
								},
							},
						},
					},
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Name:      "3",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "124",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "some-job-for-different-pr",
								},
							},
						},
					},
				).Build(),
				namespace: "ci",
			},
			expected: []string{"some-job", "another-job", "different-job"},
		},
		{
			name:     "no jobs for PR",
			org:      "org",
			repo:     "repo",
			prNumber: 123,
			s: &server{
				kubeClient: fakeclient.NewClientBuilder().WithRuntimeObjects(
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "124",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "some-job-for-different-pr",
								},
							},
						},
					},
				).Build(),
				namespace: "ci",
			},
		},
		{
			name:     "doesn't pick up completed jobs",
			org:      "org",
			repo:     "repo",
			prNumber: 123,
			s: &server{
				kubeClient: fakeclient.NewClientBuilder().WithRuntimeObjects(
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "123",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.AbortedState},
									ProwJob: "aborted-job",
								},
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.SuccessState},
									ProwJob: "succeeded-job",
								},
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.ErrorState},
									ProwJob: "errored-job",
								},
							},
						},
					},
				).Build(),
				namespace: "ci",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jobs, err := tc.s.getPayloadJobsForPR(tc.org, tc.repo, tc.prNumber, logrus.NewEntry(nil))
			if err != nil {
				t.Fatalf("couldn't get jobs")
			}
			if diff := cmp.Diff(tc.expected, jobs); diff != "" {
				t.Fatalf("actual jobs don't match expected, diff: %s", diff)
			}
		})
	}
}

func fakeResolve(ocp string, releaseType api.ReleaseStream, jobType config.JobType) []string {
	return []string{fmt.Sprintf("dummy-ocp-%s-%s-%s-job1", ocp, releaseType, jobType), fmt.Sprintf("dummy-ocp-%s-%s-%s-job2", ocp, releaseType, jobType)}
}

func init() {
	if err := prpqv1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register prpqv1 scheme: %v", err))
	}
}

func TestBuild(t *testing.T) {
	testCases := []struct {
		name          string
		spec          jobSetSpecification
		jobTuples     []prpqv1.ReleaseJobSpec
		additionalPRs []prpqv1.PullRequestUnderTest
		expected      *prpqv1.PullRequestPayloadQualificationRun
	}{
		{
			name: "basic case",
			spec: jobSetSpecification{
				ocp:         "4.10",
				releaseType: "nightly",
				jobs:        "ci",
			},
			jobTuples: []prpqv1.ReleaseJobSpec{
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test:            "e2e-aws-serial",
					AggregatedCount: 5,
				},
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test:            "e2e-metal-ipi",
					AggregatedCount: 10,
				},
			},
			expected: &prpqv1.PullRequestPayloadQualificationRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-guid-0",
					Namespace: "ci",
					Labels: map[string]string{
						"dptp.openshift.io/requester": "payload-testing",
						"event-GUID":                  "some-guid",
						"prow.k8s.io/refs.org":        "org",
						"prow.k8s.io/refs.pull":       "123",
						"prow.k8s.io/refs.repo":       "repo",
						"prow.k8s.io/refs.base_ref":   "ref",
					},
				},
				Spec: prpqv1.PullRequestPayloadTestSpec{
					PullRequests: []prpqv1.PullRequestUnderTest{{Org: "org",
						Repo:        "repo",
						BaseRef:     "ref",
						BaseSHA:     "sha",
						PullRequest: &prpqv1.PullRequest{Number: 123, Author: "login", SHA: "head-sha", Title: "title"}}},
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						ReleaseControllerConfig: prpqv1.ReleaseControllerConfig{OCP: "4.10", Release: "nightly", Specifier: "ci"},
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
								AggregatedCount:  5,
							},
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-metal-ipi",
								AggregatedCount:  10,
							},
						},
					},
				},
			},
		},
		{
			name: "empty spec",
			spec: jobSetSpecification{},
			jobTuples: []prpqv1.ReleaseJobSpec{
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test: "e2e-aws-serial",
				},
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test: "e2e-metal-ipi",
				},
			},
			expected: &prpqv1.PullRequestPayloadQualificationRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-guid-0",
					Namespace: "ci",
					Labels: map[string]string{
						"dptp.openshift.io/requester": "payload-testing",
						"event-GUID":                  "some-guid",
						"prow.k8s.io/refs.org":        "org",
						"prow.k8s.io/refs.pull":       "123",
						"prow.k8s.io/refs.repo":       "repo",
						"prow.k8s.io/refs.base_ref":   "ref",
					},
				},
				Spec: prpqv1.PullRequestPayloadTestSpec{
					PullRequests: []prpqv1.PullRequestUnderTest{{Org: "org",
						Repo:        "repo",
						BaseRef:     "ref",
						BaseSHA:     "sha",
						PullRequest: &prpqv1.PullRequest{Number: 123, Author: "login", SHA: "head-sha", Title: "title"}}},
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
							},
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-metal-ipi",
							},
						},
					},
				},
			},
		},
		{
			name: "additional PRs",
			spec: jobSetSpecification{
				ocp:         "4.10",
				releaseType: "nightly",
				jobs:        "ci",
			},
			jobTuples: []prpqv1.ReleaseJobSpec{
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test:            "e2e-aws-serial",
					AggregatedCount: 5,
				},
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "main",
						Variant: "nightly-4.10",
					},
					Test:            "e2e-metal-ipi",
					AggregatedCount: 10,
				},
			},
			additionalPRs: []prpqv1.PullRequestUnderTest{
				{
					Org:     "org",
					Repo:    "other-repo",
					BaseRef: "base",
					BaseSHA: "BASE",
					PullRequest: &prpqv1.PullRequest{
						Number: 1234,
						Author: "a-developer",
						SHA:    "HEAD",
						Title:  "some PR",
					},
				},
			},
			expected: &prpqv1.PullRequestPayloadQualificationRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-guid-0",
					Namespace: "ci",
					Labels: map[string]string{
						"dptp.openshift.io/requester": "payload-testing",
						"event-GUID":                  "some-guid",
						"prow.k8s.io/refs.org":        "org",
						"prow.k8s.io/refs.pull":       "123",
						"prow.k8s.io/refs.repo":       "repo",
						"prow.k8s.io/refs.base_ref":   "ref",
					},
				},
				Spec: prpqv1.PullRequestPayloadTestSpec{
					PullRequests: []prpqv1.PullRequestUnderTest{
						{
							Org:         "org",
							Repo:        "other-repo",
							BaseRef:     "base",
							BaseSHA:     "BASE",
							PullRequest: &prpqv1.PullRequest{Number: 1234, Author: "a-developer", SHA: "HEAD", Title: "some PR"},
						},
						{
							Org:         "org",
							Repo:        "repo",
							BaseRef:     "ref",
							BaseSHA:     "sha",
							PullRequest: &prpqv1.PullRequest{Number: 123, Author: "login", SHA: "head-sha", Title: "title"},
						},
					},
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						ReleaseControllerConfig: prpqv1.ReleaseControllerConfig{OCP: "4.10", Release: "nightly", Specifier: "ci"},
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
								AggregatedCount:  5,
							},
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "main", Variant: "nightly-4.10"},
								Test:             "e2e-metal-ipi",
								AggregatedCount:  10,
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			builder := &prpqrBuilder{
				namespace: "ci",
				org:       "org",
				repo:      "repo",
				prNumber:  123,
				guid:      "some-guid",
				pr: &github.PullRequest{
					Base: github.PullRequestBranch{
						Ref: "ref",
						SHA: "sha",
					},
					Title: "title",
					Head: github.PullRequestBranch{
						SHA: "head-sha",
					},
					User: github.User{
						Login: "login",
					},
				},
				spec: tc.spec,
			}
			actual := builder.build(tc.jobTuples, tc.additionalPRs)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

type fakeTrustedChecker struct {
}

func (c *fakeTrustedChecker) trustedUser(author, _, _ string, _ int) (bool, error) {
	if strings.Contains(author, "not-trusted") {
		return false, nil
	}
	return true, nil
}

func TestHandle(t *testing.T) {
	ghc := fakegithub.NewFakeClient()
	pr123 := github.PullRequest{}
	ghc.PullRequests = map[int]*github.PullRequest{
		123: &pr123,
		999: {},
	}

	testCases := []struct {
		name                  string
		s                     *server
		ic                    github.IssueCommentEvent
		expectedMessage       string
		expectedAdditionalPRs []config.AdditionalPR
	}{
		{
			name: "basic case",
			s: &server{
				ghc:        ghc,
				ctx:        context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().Build(),
				namespace:  "ci",
				jobResolver: newFakeJobResolver(map[string][]config.Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
				}}),
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload 4.10 nightly informing",
				},
			},
			expectedMessage: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
		},
		{
			name: "payload with prs",
			s: &server{
				ghc:        ghc,
				ctx:        context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().Build(),
				namespace:  "ci",
				jobResolver: newFakeJobResolver(map[string][]config.Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
				}}),
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-with-prs 4.10 nightly informing openshift/kubernetes#999",
				},
			},
			expectedMessage: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
			expectedAdditionalPRs: []config.AdditionalPR{"openshift/kubernetes#999"},
		},
		{
			name: "payload-job with sharded job",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-job periodic-ci-openshift-release-master-nightly-4.10-e2e-sharded-1of3",
				},
			},
			expectedMessage: `trigger 1 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-sharded-1of3

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
		},
		{
			name: "payload-job",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-job periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial periodic-ci-openshift-release-another-job",
				},
			},
			expectedMessage: `trigger 1 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
		},
		{
			name: "payload-aggregate",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/payload-aggregate periodic-ci-openshift-release-master-nightly-4.10-e2e-sharded-1of3 10`,
				},
			},
			expectedMessage: `trigger 1 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-sharded-1of3

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
		},
		{
			name: "payload-aggregate with sharded job",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/payload-aggregate periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial 10
/payload-aggregate periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi 10`,
				},
			},
			expectedMessage: `trigger 2 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
		},
		{
			name: "payload-aggregate-with-prs",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/payload-aggregate-with-prs periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial 10 openshift/kubernetes#999`,
				},
			},
			expectedMessage: `trigger 1 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
			expectedAdditionalPRs: []config.AdditionalPR{"openshift/kubernetes#999"},
		},
		{
			name: "payload-job-with-prs",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-job-with-prs periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial openshift/kubernetes#999",
				},
			},
			expectedMessage: `trigger 1 job(s) for the /payload-(with-prs|job|aggregate|job-with-prs|aggregate-with-prs) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
			expectedAdditionalPRs: []config.AdditionalPR{"openshift/kubernetes#999"},
		},
		{
			name: "payload-job-with-prs incorrectly issued multiple times in the same comment",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/payload-job-with-prs periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial openshift/kubernetes#999
/payload-job-with-prs periodic-ci-openshift-release-master-nightly-4.11-e2e-aws-serial openshift/kubernetes#999`,
				},
			},
			expectedMessage: "given command is invalid: at least one of the commands given is only supported on a one-command-per-comment basis, please separate out commands as multiple comments",
		},
		{
			name: "multiple singular commands incorrectly issued in the same comment",
			s: &server{
				ghc:                ghc,
				ctx:                context.TODO(),
				kubeClient:         fakeclient.NewClientBuilder().Build(),
				namespace:          "ci",
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/payload-job-with-prs periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial openshift/kubernetes#999
/payload-with-prs informing openshift/kubernetes#999
/payload-aggregate-with-prs periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial 10 openshift/kubernetes#999
`,
				},
			},
			expectedMessage: "given command is invalid: at least one of the commands given is only supported on a one-command-per-comment basis, please separate out commands as multiple comments",
		},
		{
			name: "non-prowgen jobs",
			s: &server{
				ghc:        ghc,
				ctx:        context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().Build(),
				namespace:  "ci",
				jobResolver: newFakeJobResolver(map[string][]config.Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
					{Name: "release-openshift-ocp-installer-e2e-azure-serial-4.10"},
				}, "4.8": {
					{Name: "some-non-prow-gen-job"},
				}}),
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload 4.10 nightly informing\n/payload 4.8 ci all",
				},
			},
			expectedMessage: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0

trigger 0 job(s) of type all for the ci release of OCP 4.8
`,
		},
		{
			name: "user is not trusted",
			s: &server{
				ghc:            ghc,
				ctx:            context.TODO(),
				namespace:      "ci",
				trustedChecker: &fakeTrustedChecker{},
			},
			ic: github.IssueCommentEvent{
				Repo: github.Repo{
					Owner: github.User{
						Login: "org",
					},
					Name: "repo",
				},
				GUID: "guid",
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					User: github.User{Login: "not-trusted"},
					Body: "/payload 4.10 nightly informing",
				},
			},
			expectedMessage: `user not-trusted is not trusted for pull request org/repo#123`,
		},
		{
			name: "not contribute to official images",
			s: &server{
				ghc:        ghc,
				ctx:        context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().Build(),
				namespace:  "ci",
				jobResolver: newFakeJobResolver(map[string][]config.Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
				}}),
				testResolver:       newFakeTestResolver(),
				trustedChecker:     &fakeTrustedChecker{},
				ciOpConfigResolver: &fakeCIOpConfigResolver{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload 4.10 nightly informing",
				},
			},
			expectedMessage: `the repo org/repo does not contribute to the OpenShift official images, or the base branch is not currently having images promoted`,
		},
		{
			name: "abort all jobs",
			s: &server{
				ghc: ghc,
				ctx: context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().WithRuntimeObjects(
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "123",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "some-job",
								},
							},
						},
					},
					&prowapi.ProwJob{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "some-job",
							Namespace: "ci",
						},
						Status: prowapi.ProwJobStatus{State: prowapi.PendingState},
					},
				).Build(),
				namespace:      "ci",
				trustedChecker: &fakeTrustedChecker{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-abort",
				},
			},
			expectedMessage: `aborted 1 active payload job(s) for pull request org/repo#123`,
		},
		{
			name: "abort all jobs aborts underlying aggregated job runs",
			s: &server{
				ghc: ghc,
				ctx: context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().WithRuntimeObjects(
					&prpqv1.PullRequestPayloadQualificationRun{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ci",
							Labels: map[string]string{
								kube.OrgLabel:  "org",
								kube.RepoLabel: "repo",
								kube.PullLabel: "123",
							},
						},
						Spec: prpqv1.PullRequestPayloadTestSpec{},
						Status: prpqv1.PullRequestPayloadTestStatus{
							Jobs: []prpqv1.PullRequestPayloadJobStatus{
								{
									Status:  prowapi.ProwJobStatus{State: prowapi.PendingState},
									ProwJob: "aggregator-some-job",
								},
							},
						},
					},
					// Aggregator job with aggregation-id label (pending - will be aborted)
					&prowapi.ProwJob{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "aggregator-some-job",
							Namespace: "ci",
							Labels: map[string]string{
								api.AggregationIDLabel: "test-aggregation-id",
							},
							Annotations: map[string]string{
								api.ProwJobJobNameAnnotation: "aggregator-some-job",
							},
						},
						Status: prowapi.ProwJobStatus{State: prowapi.PendingState},
					},
					// First underlying aggregated job (pending - will be aborted)
					&prowapi.ProwJob{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "some-job-0",
							Namespace: "ci",
							Labels: map[string]string{
								api.AggregationIDLabel: "test-aggregation-id",
							},
						},
						Status: prowapi.ProwJobStatus{State: prowapi.PendingState},
					},
					// Second underlying aggregated job (triggered - will be aborted)
					&prowapi.ProwJob{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "some-job-1",
							Namespace: "ci",
							Labels: map[string]string{
								api.AggregationIDLabel: "test-aggregation-id",
							},
						},
						Status: prowapi.ProwJobStatus{State: prowapi.TriggeredState},
					},
					// Third underlying aggregated job (already completed - will NOT be counted)
					&prowapi.ProwJob{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "some-job-2",
							Namespace: "ci",
							Labels: map[string]string{
								api.AggregationIDLabel: "test-aggregation-id",
							},
						},
						Status: prowapi.ProwJobStatus{
							State:          prowapi.SuccessState,
							CompletionTime: &metav1.Time{Time: time.Now()},
						},
					},
				).Build(),
				namespace:      "ci",
				trustedChecker: &fakeTrustedChecker{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-abort",
				},
			},
			expectedMessage: `aborted 3 active payload job(s) for pull request org/repo#123`,
		},
		{
			name: "incorrectly formatted command",
			s: &server{
				ghc:            ghc,
				ctx:            context.TODO(),
				namespace:      "ci",
				trustedChecker: &fakeTrustedChecker{},
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "org"}, Name: "repo"},
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload-something-that-doesnt-exist 4.10 nightly informing",
				},
			},
			expectedMessage: `it appears that you have attempted to use some version of the payload command, but your comment was incorrectly formatted and cannot be acted upon. See the [docs](https://docs.ci.openshift.org/release-oversight/payload-testing/#usage) for usage info.`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualMessage, actualAddtionalPRs := tc.s.handle(logrus.WithField("tc.name", tc.name), tc.ic)
			if diff := cmp.Diff(tc.expectedMessage, actualMessage); diff != "" {
				t.Errorf("%s differs from expectedMessage:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedAdditionalPRs, actualAddtionalPRs, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("%s differs from expectedAdditionalPRs:\n%s", tc.name, diff)
			}
		})
	}
}

type fakeJobResolver struct {
	jobs map[string][]config.Job
}

func newFakeJobResolver(jobs map[string][]config.Job) jobResolver {
	return &fakeJobResolver{jobs: jobs}
}

func (r *fakeJobResolver) resolve(ocp string, _ api.ReleaseStream, _ config.JobType) ([]config.Job, error) {
	return r.jobs[ocp], nil
}

type fakeTestResolver struct {
	tuples map[string]api.MetadataWithTest
}

func newFakeTestResolver() testResolver {
	return &fakeTestResolver{
		tuples: map[string]api.MetadataWithTest{
			"periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial": {
				Metadata: api.Metadata{
					Org:     "openshift",
					Repo:    "release",
					Branch:  "master",
					Variant: "nightly-4.10",
				},
				Test: "e2e-aws-serial",
			},
			"periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi": {
				Metadata: api.Metadata{
					Org:     "openshift",
					Repo:    "release",
					Branch:  "master",
					Variant: "nightly-4.10",
				},
				Test: "e2e-metal-ipi",
			},
			"periodic-ci-openshift-release-master-nightly-4.10-e2e-sharded": {
				Metadata: api.Metadata{
					Org:     "openshift",
					Repo:    "release",
					Branch:  "master",
					Variant: "nightly-4.10",
				},
				Test: "e2e-sharded",
			},
		},
	}
}

func (r *fakeTestResolver) resolve(job string) (api.MetadataWithTest, error) {
	// Remove shard suffix from job if present prior to searching for it in map
	baseJob := shardSuffixPattern.ReplaceAllString(job, "")
	if jt, ok := r.tuples[baseJob]; ok {
		return jt, nil
	}
	return api.MetadataWithTest{}, fmt.Errorf("failed to resolve job %s", job)
}

func TestFormatError(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "could not create PullRequestPayloadQualificationRun: context canceled",
			err:  fmt.Errorf("could not create PullRequestPayloadQualificationRun: %w", fmt.Errorf("context canceled")),
			expected: `An error was encountered. We were able to detect the following conditions from the error:

- The pod running the tool gets restarted. Please try again later.


<details><summary>Full error message.</summary>

<code>
could not create PullRequestPayloadQualificationRun: context canceled
</code>

</details>

Please contact an administrator to resolve this issue.`,
		},
		{
			name: "unknown error",
			err:  fmt.Errorf("unknown error"),
			expected: `An error was encountered. No known errors were detected, please see the full error message for details.

<details><summary>Full error message.</summary>

<code>
unknown error
</code>

</details>

Please contact an administrator to resolve this issue.`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := formatError(tc.err)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

type fakeCIOpConfigResolver struct {
}

func (r fakeCIOpConfigResolver) Config(m *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
	if m == nil {
		return nil, fmt.Errorf("some error")
	}
	if m.Org == "openshift" {
		return &api.ReleaseBuildConfiguration{
			PromotionConfiguration: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Namespace: "ocp"}}},
		}, nil
	}
	return &api.ReleaseBuildConfiguration{}, nil
}

func makeTrustedApps(slugs ...string) prowflagutil.Strings {
	var s prowflagutil.Strings
	for _, slug := range slugs {
		_ = s.Set(slug)
	}
	return s
}

func TestGithubTrustedChecker_isTrustedApp(t *testing.T) {
	c := &githubTrustedChecker{
		githubClient: nil,
		trustedApps:  makeTrustedApps("openshift-pr-manager[bot]", "another-trusted-app"),
	}

	tests := []struct {
		login   string
		allowed bool
	}{
		{"openshift-pr-manager[bot]", true},
		{"another-trusted-app", true},
		{"untrusted-app[bot]", false},
	}

	for _, tt := range tests {
		got := c.isTrustedApp(tt.login)
		if got != tt.allowed {
			t.Fatalf("isTrustedApp(%q)=%v, want %v", tt.login, got, tt.allowed)
		}
	}
}

func TestGithubTrustedChecker_trustedUser_AllowsTrustedApp(t *testing.T) {
	c := &githubTrustedChecker{
		githubClient: nil,
		trustedApps:  makeTrustedApps("openshift-pr-manager[bot]"),
	}

	trusted, err := c.trustedUser("openshift-pr-manager[bot]", "openshift", "ovn-kubernetes", 0)
	if err != nil {
		t.Fatalf("trustedUser returned error: %v", err)
	}
	if !trusted {
		t.Fatalf("trustedUser should trust allowed app installation user")
	}
}

func TestGithubTrustedChecker_trustedUser_UntrustedAppFallsBackToHumanCheck(t *testing.T) {
	c := &githubTrustedChecker{
		githubClient: nil,
		trustedApps:  makeTrustedApps("some-other-trusted-app"),
	}

	trusted, err := c.trustedUser("random-untrusted-bot[bot]", "openshift", "ovn-kubernetes", 0)
	if err != nil {
		t.Fatalf("trustedUser returned error: %v", err)
	}
	if trusted {
		t.Fatalf("trustedUser unexpectedly trusted unlisted app installation user")
	}
}
