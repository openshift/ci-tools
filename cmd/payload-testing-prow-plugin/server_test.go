package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
	"k8s.io/test-infra/prow/kube"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			name:     "/payload 4.10 nightly informing",
			comment:  "/payload 4.10 nightly informing",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}},
		},
		{
			name:     "/payload 4.8 ci all",
			comment:  "/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.8", releaseType: "ci", jobs: "all"}},
		},
		{
			name:    "/cmd 4.8 ci all",
			comment: "/cmd 4.8 ci all",
		},
		{
			name:     "multiple match",
			comment:  "/payload 4.10 nightly informing\n/payload 4.8 ci all",
			expected: []jobSetSpecification{{ocp: "4.10", releaseType: "nightly", jobs: "informing"}, {ocp: "4.8", releaseType: "ci", jobs: "all"}},
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
			name:     "/payload-job periodic-ci-openshift-release-some-job",
			comment:  "/payload-job periodic-ci-openshift-release-some-job",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}},
		},
		{
			name:     "multiple job",
			comment:  "/payload-job periodic-ci-openshift-release-some-job periodic-ci-openshift-release-another-job",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}, {Name: "periodic-ci-openshift-release-another-job"}},
		},
		{
			name:     "multiple match",
			comment:  "/payload-job periodic-ci-openshift-release-some-job periodic-ci-openshift-release-another-job\n/cmd 4.8 ci all\n/payload-job periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job"}, {Name: "periodic-ci-openshift-release-another-job"}, {Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"}},
		},
		{
			name:     "/payload-aggregate periodic-ci-openshift-release-some-job   10  ",
			comment:  "/payload-aggregate periodic-ci-openshift-release-some-job   10  ",
			expected: []config.Job{{Name: "periodic-ci-openshift-release-some-job", AggregatedCount: 10}},
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
			name:     "jobs exist",
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
									ProwJob: "some-job",
								},
								{
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
									ProwJob: "some-job-for-different-pr",
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
			jobs, err := tc.s.getPayloadJobsForPR(tc.org, tc.repo, tc.prNumber)
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
		name      string
		spec      jobSetSpecification
		jobTuples []prpqv1.ReleaseJobSpec
		expected  *prpqv1.PullRequestPayloadQualificationRun
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
						Branch:  "master",
						Variant: "nightly-4.10",
					},
					Test:            "e2e-aws-serial",
					AggregatedCount: 5,
				},
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "master",
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
					PullRequest: prpqv1.PullRequestUnderTest{Org: "org",
						Repo:        "repo",
						BaseRef:     "ref",
						BaseSHA:     "sha",
						PullRequest: prpqv1.PullRequest{Number: 123, Author: "login", SHA: "head-sha", Title: "title"}},
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						ReleaseControllerConfig: prpqv1.ReleaseControllerConfig{OCP: "4.10", Release: "nightly", Specifier: "ci"},
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
								AggregatedCount:  5,
							},
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
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
						Branch:  "master",
						Variant: "nightly-4.10",
					},
					Test: "e2e-aws-serial",
				},
				{
					CIOperatorConfig: prpqv1.CIOperatorMetadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "master",
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
					PullRequest: prpqv1.PullRequestUnderTest{Org: "org",
						Repo:        "repo",
						BaseRef:     "ref",
						BaseSHA:     "sha",
						PullRequest: prpqv1.PullRequest{Number: 123, Author: "login", SHA: "head-sha", Title: "title"}},
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
							},
							{
								CIOperatorConfig: prpqv1.CIOperatorMetadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
								Test:             "e2e-metal-ipi",
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
			actual := builder.build(tc.jobTuples)
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
	ghc.PullRequests = map[int]*github.PullRequest{123: &pr123}

	testCases := []struct {
		name     string
		s        *server
		ic       github.IssueCommentEvent
		expected string
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
			expected: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

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
			expected: `trigger 1 job(s) for the /payload-(job|aggregate) command
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
					Body: `/payload-aggregate periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial 10
/payload-aggregate periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi 10`,
				},
			},
			expected: `trigger 2 job(s) for the /payload-(job|aggregate) command
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

See details on https://pr-payload-tests.ci.openshift.org/runs/ci/guid-0
`,
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
			expected: `trigger 2 job(s) of type informing for the nightly release of OCP 4.10
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
			expected: `user not-trusted is not trusted for pull request org/repo#123`,
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
			expected: `the repo org/repo does not contribute to the OpenShift official images`,
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
			expected: `aborted active payload jobs for pull request org/repo#123`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.s.handle(logrus.WithField("tc.name", tc.name), tc.ic)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
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
		},
	}
}

func (r *fakeTestResolver) resolve(job string) (api.MetadataWithTest, error) {
	if jt, ok := r.tuples[job]; ok {
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
			PromotionConfiguration: &api.PromotionConfiguration{Namespace: "ocp"},
		}, nil
	}
	return &api.ReleaseBuildConfiguration{}, nil
}
