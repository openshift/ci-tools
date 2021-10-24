package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
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

func TestMessage(t *testing.T) {
	testCases := []struct {
		name     string
		spec     jobSetSpecification
		expected string
	}{
		{
			name: "basic case",
			spec: jobSetSpecification{ocp: "4.10", releaseType: "nightly", jobs: "informing"},
			expected: `trigger 2 jobs of type informing for the nightly release of OCP 4.10
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

func fakeResolve(ocp string, releaseType releaseType, jobType jobType) []string {
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
		b         *prpqrBuilder
		jobTuples []api.MetadataWithTest
		expected  *prpqv1.PullRequestPayloadQualificationRun
	}{
		{
			name: "basic case",
			jobTuples: []api.MetadataWithTest{
				{
					Metadata: api.Metadata{
						Org:     "openshift",
						Repo:    "release",
						Branch:  "master",
						Variant: "nightly-4.10",
					},
					Test: "e2e-aws-serial",
				},
				{
					Metadata: api.Metadata{
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
					},
				},
				Spec: prpqv1.PullRequestPayloadTestSpec{
					Jobs: prpqv1.PullRequestPayloadJobSpec{
						Jobs: []prpqv1.ReleaseJobSpec{
							{
								CIOperatorConfig: api.Metadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
								Test:             "e2e-aws-serial",
							},
							{
								CIOperatorConfig: api.Metadata{Org: "openshift", Repo: "release", Branch: "master", Variant: "nightly-4.10"},
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
			}
			actual := builder.build(tc.jobTuples)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
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
				jobResolver: newFakeJobResolver(map[string][]Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
				}}),
				testResolver: newFakeTestResolver(),
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload 4.10 nightly informing",
				},
			},
			expected: `trigger 2 jobs of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi
`,
		},
		{
			name: "non-prowgen jobs",
			s: &server{
				ghc:        ghc,
				ctx:        context.TODO(),
				kubeClient: fakeclient.NewClientBuilder().Build(),
				namespace:  "ci",
				jobResolver: newFakeJobResolver(map[string][]Job{"4.10": {
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
					{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
					{Name: "release-openshift-ocp-installer-e2e-azure-serial-4.10"},
				}, "4.8": {
					{Name: "some-non-prow-gen-job"},
				}}),
				testResolver: newFakeTestResolver(),
			},
			ic: github.IssueCommentEvent{
				GUID: "guid",
				Issue: github.Issue{
					Number:      123,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/payload 4.10 nightly informing\n/payload 4.8 ci all",
				},
			},
			expected: `trigger 2 jobs of type informing for the nightly release of OCP 4.10
- periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial
- periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi

trigger 0 jobs of type all for the ci release of OCP 4.8
`,
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
	jobs map[string][]Job
}

func newFakeJobResolver(jobs map[string][]Job) jobResolver {
	return &fakeJobResolver{jobs: jobs}
}

func (r *fakeJobResolver) resolve(ocp string, _ releaseType, _ jobType) ([]Job, error) {
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
