package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakeGithubClient struct {
	prs map[string]*github.PullRequest
	// refErrors maps "org/repo:ref" to the error GetRef returns for it.
	refErrors map[string]error
}

func (c fakeGithubClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	orgRepoNumber := fmt.Sprintf("%s/%s#%d", org, repo, number)
	return c.prs[orgRepoNumber], nil
}

func (c fakeGithubClient) CreateComment(owner, repo string, number int, comment string) error {
	//no-op
	return nil
}

func (c fakeGithubClient) GetRef(org, repo, ref string) (string, error) {
	if err, ok := c.refErrors[fmt.Sprintf("%s/%s:%s", org, repo, ref)]; ok {
		return "", err
	}
	return ref, nil
}

type fakeTrustedChecker struct {
}

func (c *fakeTrustedChecker) trustedUser(author, _, _ string, _ int) (bool, error) {
	if strings.Contains(author, "not-trusted") {
		return false, nil
	}
	return true, nil
}

type fakeReporter struct {
	reported []*prowv1.ProwJob
	mutex    sync.Mutex
}

func (r *fakeReporter) reportNewProwJob(prowJob *prowv1.ProwJob, jr jobRun, logger *logrus.Entry) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.reported = append(r.reported, prowJob)
	return nil
}

func (r *fakeReporter) sync(logger *logrus.Entry) error {
	//no-op
	return nil
}

func TestHandle(t *testing.T) {
	testCases := []struct {
		name         string
		issueComment github.IssueCommentEvent
		originPR     github.PullRequest
		kubeClient   ctrlruntimeclient.Client
		expected     []prowv1.ProwJob
		expectedErr  error
		abortComment string
	}{
		{
			name:         "trigger a single multi-pr job run with additional PR from the same repo",
			abortComment: abortCommand,
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit openshift/ci-tools#123",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewFakeClient(),
		},
		{
			name: "trigger a single multi-pr job run with additional PR from a different repo",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit openshift/release#876",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewFakeClient(),
		},
		{
			name: "trigger multiple multi-pr job runs with diverse additional PRs",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: `/testwith openshift/ci-tools/main/unit openshift/ci-tools#123
/testwith openshift/ci-tools/main/e2e openshift/release#876
/testwith openshift/ci-tools/main/unit https://github.com/openshift/release/pull/876`,
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewFakeClient(),
		},
		{
			name: "too many PRs, expect error",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient:  fakectrlruntimeclient.NewFakeClient(),
			expectedErr: errors.New("could not determine job runs: 24 PRs found which is more than the max of 20, will not process request"),
		},
		{
			name: "untrusted user, expect error",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit openshift/release#876",
					User: github.User{Login: "not-trusted"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient:  fakectrlruntimeclient.NewFakeClient(),
			expectedErr: errors.New("the user: not-trusted is not trusted to trigger tests"),
		},
		{
			name:         "trigger a PR-free multi-pr job run for a test in the origin repo",
			abortComment: "/testwith  abort",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewFakeClient(),
		},
		{
			name:         "trigger a PR-free multi-pr job run for a test in a different repo",
			abortComment: "/testwith\tabort \r\n",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/release/main/integration",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewFakeClient(),
		},
		{
			name: "abort followed by other content is rejected",
			issueComment: github.IssueCommentEvent{
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith  abort\nthanks",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
					Ref:  "main",
				},
			},
			kubeClient:  fakectrlruntimeclient.NewFakeClient(),
			expectedErr: errors.New("could not determine job runs: /testwith abort must be the only content of the comment"),
		},
		{
			name: "abort with trailing whitespace is not parsed as a test request",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith abort \r\n",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-pr-job-1",
						Namespace: "ci",
						Labels: map[string]string{
							kube.OrgLabel:         "openshift",
							kube.RepoLabel:        "ci-tools",
							kube.PullLabel:        strconv.Itoa(999),
							kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
							testwithLabel:         "openshift.ci-tools.999",
						},
					},
					Spec: prowv1.ProwJobSpec{
						Job: "multi-pr-job-1",
					},
					Status: prowv1.ProwJobStatus{State: prowv1.PendingState},
				},
			).Build(),
		},
		{
			name: "abort multi-pr jobs",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith abort",
					User: github.User{Login: "developer"},
				},
			},
			originPR: github.PullRequest{
				Number: 999,
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
			},
			kubeClient: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-pr-job-1",
						Namespace: "ci",
						Labels: map[string]string{
							kube.OrgLabel:         "openshift",
							kube.RepoLabel:        "ci-tools",
							kube.PullLabel:        strconv.Itoa(999),
							kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
							testwithLabel:         "openshift.ci-tools.999",
						},
					},
					Spec: prowv1.ProwJobSpec{
						Job: "multi-pr-job-1",
					},
					Status: prowv1.ProwJobStatus{State: prowv1.PendingState},
				},
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-pr-job-2",
						Namespace: "ci",
						Labels: map[string]string{
							kube.OrgLabel:         "openshift",
							kube.RepoLabel:        "ci-tools",
							kube.PullLabel:        strconv.Itoa(999),
							kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
							testwithLabel:         "openshift.ci-tools.999",
						},
					},
					Spec: prowv1.ProwJobSpec{
						Job: "multi-pr-job-2",
					},
					Status: prowv1.ProwJobStatus{State: prowv1.SuccessState, CompletionTime: &metav1.Time{Time: time.Now()}},
				},
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-pr-job-3",
						Namespace: "ci",
						Labels: map[string]string{
							kube.OrgLabel:         "openshift",
							kube.RepoLabel:        "ci-tools",
							kube.PullLabel:        strconv.Itoa(999),
							kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
							testwithLabel:         "openshift.ci-tools.999",
						},
					},
					Spec: prowv1.ProwJobSpec{
						Job: "multi-pr-job-3",
					},
					Status: prowv1.ProwJobStatus{State: prowv1.TriggeredState},
				},
				&prowv1.ProwJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-presubmit",
						Namespace: "ci",
						Labels: map[string]string{
							kube.OrgLabel:         "openshift",
							kube.RepoLabel:        "ci-tools",
							kube.PullLabel:        strconv.Itoa(999),
							kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
						},
					},
					Spec: prowv1.ProwJobSpec{
						Job: "regular-presubmit",
					},
					Status: prowv1.ProwJobStatus{State: prowv1.PendingState},
				},
			).Build(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			org := tc.issueComment.Repo.Owner.Login
			repo := tc.issueComment.Repo.Name
			orginPRRef := fmt.Sprintf("%s/%s#%d", org, repo, tc.issueComment.Issue.Number)
			fghc := fakeGithubClient{prs: map[string]*github.PullRequest{
				orginPRRef: &tc.originPR,
				"openshift/ci-tools#123": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "main",
					},
					Number: 123,
				},
				"openshift/release#876": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "release",
						},
						Ref: "main",
					},
					Number: 876,
				},
			}}
			originMetadata := api.Metadata{
				Org:    org,
				Repo:   repo,
				Branch: tc.originPR.Base.Ref,
			}
			s := server{
				ciOpConfigResolver: &fakeCIOpConfigResolver{
					configs: map[api.Metadata]*api.ReleaseBuildConfiguration{
						originMetadata: {
							Tests: []api.TestStepConfiguration{
								{
									As: "unit",
								},
								{
									As: "e2e",
								},
							},
						},
						{Org: "openshift", Repo: "release", Branch: "main"}: {
							Tests: []api.TestStepConfiguration{
								{
									As: "integration",
								},
							},
						},
					},
				},
				prowConfigGetter: &fakeProwConfigGetter{
					cfg: &prowconfig.Config{
						ProwConfig: prowconfig.ProwConfig{
							Scheduler: prowconfig.Scheduler{Enabled: false},
						},
					},
				},
				namespace:        "ci",
				dispatcherClient: &fakeDispatcherClient{},
				jobClusterCache: jobClusterCache{
					clusterForJob: map[string]string{
						"pull-ci-openshift-ci-tools-main-unit": "build01",
						"pull-ci-openshift-ci-tools-main-e2e":  "build02",
					},
					lastCleared: time.Now(),
				},
				ghc:            fghc,
				trustedChecker: &fakeTrustedChecker{},
				kubeClient:     tc.kubeClient,
				reporter:       &fakeReporter{},
			}

			prowJobs, err := s.handle(logrus.NewEntry(logrus.StandardLogger()), tc.issueComment)
			if err != nil {
				if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
					t.Fatalf("Unexpected error (-want, +got): %v", diff)
				}
			} else {
				if tc.abortComment != "" {
					if len(prowJobs) != 1 {
						t.Fatalf("expected one created job, got %d", len(prowJobs))
					}
					abortComment := tc.issueComment
					abortComment.Comment.Body = tc.abortComment
					abortedJobs, err := s.handle(logrus.NewEntry(logrus.StandardLogger()), abortComment)
					if err != nil {
						t.Fatalf("aborting created job: %v", err)
					}
					if len(abortedJobs) != 1 {
						t.Fatalf("expected one aborted job, got %d", len(abortedJobs))
					}
					if abortedJobs[0].Name != prowJobs[0].Name {
						t.Errorf("expected aborted job %q, got %q", prowJobs[0].Name, abortedJobs[0].Name)
					}
					var storedJob prowv1.ProwJob
					if err := s.kubeClient.Get(context.Background(), ctrlruntimeclient.ObjectKeyFromObject(prowJobs[0]), &storedJob); err != nil {
						t.Fatalf("getting created job: %v", err)
					}
					if storedJob.Status.State != prowv1.AbortedState {
						t.Errorf("expected job state %q, got %q", prowv1.AbortedState, storedJob.Status.State)
					}
				}
				for _, prowJob := range prowJobs {
					defaultProwJobFields(prowJob)
				}
				testhelper.CompareWithFixture(t, prowJobs)
			}

		})
	}
}

func TestHandleIssueCommentIgnoresNonCreatedActions(t *testing.T) {
	ghc := fakeGithubClient{}
	reporter := &fakeReporter{}
	s := server{
		ghc:            ghc,
		trustedChecker: &fakeTrustedChecker{},
		reporter:       reporter,
		kubeClient:     fakectrlruntimeclient.NewFakeClient(),
		namespace:      "ci",
	}
	for _, action := range []github.IssueCommentEventAction{github.IssueCommentActionEdited, github.IssueCommentActionDeleted} {
		t.Run(string(action), func(t *testing.T) {
			s.handleIssueComment(logrus.WithField("test", t.Name()), github.IssueCommentEvent{
				Action: action,
				Repo:   github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/main/unit openshift/ci-tools#123",
					User: github.User{Login: "developer"},
				},
			})
			if got := len(reporter.reported); got != 0 {
				t.Errorf("expected no reported prow jobs for %s action, got %d", action, got)
			}
		})
	}
}

func TestDetermineJobRuns(t *testing.T) {
	testCases := []struct {
		name          string
		comment       string
		originPR      github.PullRequest
		expected      []jobRun
		expectedError error
	}{
		{
			name:    "trigger a single job with an additional PR from the same repo",
			comment: "/testwith openshift/ci-tools/main/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
					},
				},
			}},
		},
		{
			name:    "trigger a single job including a variant",
			comment: "/testwith openshift/ci-tools/main/variant/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:     "openshift",
						Repo:    "ci-tools",
						Branch:  "main",
						Variant: "variant",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
					},
				},
			}},
		},
		{
			name: "trigger multiple jobs with an additional PR from the same repo",
			comment: `/testwith openshift/ci-tools/main/unit openshift/ci-tools#123
/testwith openshift/ci-tools/main/e2e https://github.com/openshift/ci-tools/pull/123`,
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{
				{
					JobMetadata: api.MetadataWithTest{
						Metadata: api.Metadata{
							Org:    "openshift",
							Repo:   "ci-tools",
							Branch: "main",
						},
						Test: "unit",
					},
					OriginPR: github.PullRequest{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
						},
						Number: 999,
					},
					AdditionalPRs: []github.PullRequest{
						{
							Base: github.PullRequestBranch{
								Repo: github.Repo{
									Owner: github.User{Login: "openshift"},
									Name:  "ci-tools",
								},
								Ref: "main",
							},
							Number: 123,
						},
					},
				},
				{
					JobMetadata: api.MetadataWithTest{
						Metadata: api.Metadata{
							Org:    "openshift",
							Repo:   "ci-tools",
							Branch: "main",
						},
						Test: "e2e",
					},
					OriginPR: github.PullRequest{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
						},
						Number: 999,
					},
					AdditionalPRs: []github.PullRequest{
						{
							Base: github.PullRequestBranch{
								Repo: github.Repo{
									Owner: github.User{Login: "openshift"},
									Name:  "ci-tools",
								},
								Ref: "main",
							},
							Number: 123,
						},
					},
				},
			},
		},
		{
			name:    "cross-repo job preserves origin base ref",
			comment: "/testwith openshift/cluster-version-operator/main/e2e openshift/cluster-version-operator#1",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "oc",
					},
					Ref: "main",
				},
				Number: 2170,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "cluster-version-operator",
						Branch: "main",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "oc",
						},
						Ref: "main",
					},
					Number: 2170,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "cluster-version-operator",
							},
							Ref: "main",
						},
						Number: 1,
					},
				},
			}},
		},
		{
			name:    "trigger a single job with multiple additional PRs",
			comment: "/testwith openshift/ci-tools/main/unit openshift/ci-tools#123 openshift/release#876",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
					},
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "release",
							},
						},
						Number: 876,
					},
				},
			}},
		},
		{
			name:    "PR-free command for a test in the origin repo",
			comment: "/testwith openshift/ci-tools/main/unit",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
				},
			}},
		},
		{
			name:    "PR-free command for a test in a different repo preserves origin base ref",
			comment: "/testwith openshift/cluster-version-operator/main/e2e",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "oc",
					},
					Ref: "main",
				},
				Number: 2170,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "cluster-version-operator",
						Branch: "main",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "oc",
						},
						Ref: "main",
					},
					Number: 2170,
				},
			}},
		},
		{
			name:    "PR-free command with a variant mixed with a command including a PR, with CRLF line endings",
			comment: "/testwith openshift/cluster-version-operator/main/okd/e2e \r\n/testwith openshift/cluster-version-operator/main/e2e openshift/cluster-version-operator#1\r\n",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "oc",
					},
					Ref: "main",
				},
				Number: 2170,
			},
			expected: []jobRun{
				{
					JobMetadata: api.MetadataWithTest{
						Metadata: api.Metadata{
							Org:     "openshift",
							Repo:    "cluster-version-operator",
							Branch:  "main",
							Variant: "okd",
						},
						Test: "e2e",
					},
					OriginPR: github.PullRequest{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "oc",
							},
							Ref: "main",
						},
						Number: 2170,
					},
				},
				{
					JobMetadata: api.MetadataWithTest{
						Metadata: api.Metadata{
							Org:    "openshift",
							Repo:   "cluster-version-operator",
							Branch: "main",
						},
						Test: "e2e",
					},
					OriginPR: github.PullRequest{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "oc",
							},
							Ref: "main",
						},
						Number: 2170,
					},
					AdditionalPRs: []github.PullRequest{
						{
							Base: github.PullRequestBranch{
								Repo: github.Repo{
									Owner: github.User{Login: "openshift"},
									Name:  "cluster-version-operator",
								},
								Ref: "main",
							},
							Number: 1,
						},
					},
				},
			},
		},
		{
			name:    "abort command followed by other content",
			comment: "/testwith abort\nthanks",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expectedError: errors.New("/testwith abort must be the only content of the comment"),
		},
		{
			name:    "invalid format for additional PR",
			comment: "/testwith openshift/ci-tools/main/unit openshift/ci-tools/123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expectedError: errors.New("invalid format for additional PR: openshift/ci-tools/123"),
		},
		{
			name:    "invalid format for job",
			comment: "/testwith openshift/ci-tools/main/blaster/faster/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expectedError: errors.New("requested job is invalid. needs to be formatted like: <org>/<repo>/<branch>/<variant?>/<job>. instead it was: openshift/ci-tools/main/blaster/faster/unit"),
		},
		{
			name:    "trigger a single job with an additional PR in the github url format",
			comment: "/testwith openshift/ci-tools/main/unit https://github.com/openshift/ci-tools/pull/123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
					Ref: "main",
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
					},
				},
			}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orginPRRef := fmt.Sprintf("%s/%s#%d", tc.originPR.Base.Repo.Owner.Login, tc.originPR.Base.Repo.Name, tc.originPR.Number)
			fghc := fakeGithubClient{prs: map[string]*github.PullRequest{
				orginPRRef: &tc.originPR,
				"openshift/ci-tools#123": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 123,
				},
				"openshift/release#876": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "release",
						},
					},
					Number: 876,
				},
				"openshift/cluster-version-operator#1": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "cluster-version-operator",
						},
						Ref: "main",
					},
					Number: 1,
				},
			}}
			s := server{
				ghc: fghc,
			}

			jobRuns, err := s.determineJobRuns(tc.comment, tc.originPR)
			if diff := cmp.Diff(err, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("eror doesn't match expected, (-want +got):\n %v", err)
			}

			if diff := cmp.Diff(tc.expected, jobRuns); diff != "" {
				t.Fatalf("job runs don't match expected, (-want +got):\n%s", diff)
			}

		})
	}
}

type fakeCIOpConfigResolver struct {
	configs         map[api.Metadata]*api.ReleaseBuildConfiguration
	clusterProfiles map[string]*api.ClusterProfile
}

func (r fakeCIOpConfigResolver) Config(m *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
	if m == nil {
		return nil, fmt.Errorf("some error")
	}

	return r.configs[*m], nil
}

func (r fakeCIOpConfigResolver) ClusterProfile(profileName string) (*api.ClusterProfile, error) {
	cp, ok := r.clusterProfiles[profileName]
	if !ok {
		return nil, fmt.Errorf("cluster profile %q not found", profileName)
	}
	return cp, nil
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
	return nil
}

type fakeDispatcherClient struct{}

func (f *fakeDispatcherClient) ClusterForJob(jobName string) (string, error) {
	if jobName == "pull-ci-openshift-ci-tools-main-missing" {
		return "", fmt.Errorf("job: %s not found", jobName)
	} else {
		return "build02", nil
	}
}

func TestGenerateProwJob(t *testing.T) {
	testCases := []struct {
		name                  string
		jobRun                jobRun
		shardCount            *int
		canonicalGoRepository *string
		refErrors             map[string]error
		expectedError         error
	}{
		{
			name: "additional PR from the same repo",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "master",
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
		},
		{
			name: "multiple additional PRs",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "master",
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "installer",
							},
							Ref: "main",
						},
						Number: 456,
						User:   github.User{Login: "third-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_OTHER_SHA"},
					},
				},
			},
		},
		{
			name:       "sharded test looks up first shard in dispatcher",
			shardCount: ptr.To(3),
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "main",
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
		},
		{
			name: "cluster not found for job",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "missing",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "master",
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
			expectedError: errors.New("could not determine cluster for job pull-ci-openshift-ci-tools-main-missing: job: pull-ci-openshift-ci-tools-main-missing not found"),
		},
		{
			name: "no PR for the repo containing the test runs it against the branch",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "release",
						Branch: "main",
					},
					Test: "check-something",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
						Ref: "master",
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
							Ref: "main",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
		},
		{
			name: "PR-free request for a test in the origin repo",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "main",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Number: 999,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
			},
		},
		{
			name: "PR-free request for a test in a different repo runs it against the branch",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "cluster-version-operator",
						Branch: "main",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "oc",
						},
						Ref: "main",
					},
					Number: 2170,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
			},
			canonicalGoRepository: ptr.To("github.com/openshift/cluster-version-operator-alias"),
		},
		{
			name: "PR-free request for a variant test in a different repo takes the path alias from the variant config",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:     "openshift",
						Repo:    "cluster-version-operator",
						Branch:  "main",
						Variant: "okd",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "oc",
						},
						Ref: "main",
					},
					Number: 2170,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
			},
			canonicalGoRepository: ptr.To("github.com/openshift/cluster-version-operator-okd-alias"),
		},
		{
			name: "PR-free request for a test in a branch that cannot be resolved",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "cluster-version-operator",
						Branch: "missing",
					},
					Test: "e2e",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "oc",
						},
						Ref: "main",
					},
					Number: 2170,
					User:   github.User{Login: "developer"},
					Head:   github.PullRequestBranch{SHA: "A_SHA"},
				},
			},
			refErrors: map[string]error{
				"openshift/cluster-version-operator:heads/missing": errors.New("not found"),
			},
			expectedError: errors.New("failed to get baseSHA of openshift/cluster-version-operator@missing, the branch containing the requested test: not found"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testConfig := api.TestStepConfiguration{
				As:         tc.jobRun.JobMetadata.Test,
				ShardCount: tc.shardCount,
			}
			ciopConfigs := map[api.Metadata]*api.ReleaseBuildConfiguration{
				tc.jobRun.JobMetadata.Metadata: {
					Tests:                 []api.TestStepConfiguration{testConfig},
					CanonicalGoRepository: tc.canonicalGoRepository,
				},
			}
			addCIOpConfigsFromPRs(ciopConfigs, tc.jobRun.AdditionalPRs)
			addCIOpConfigsFromPRs(ciopConfigs, []github.PullRequest{tc.jobRun.OriginPR})
			s := server{
				ghc:                &fakeGithubClient{refErrors: tc.refErrors},
				ciOpConfigResolver: &fakeCIOpConfigResolver{configs: ciopConfigs},
				prowConfigGetter: &fakeProwConfigGetter{
					cfg: &prowconfig.Config{
						ProwConfig: prowconfig.ProwConfig{
							Scheduler: prowconfig.Scheduler{Enabled: false},
						},
					},
				},
				namespace:        "ci",
				dispatcherClient: &fakeDispatcherClient{},
				jobClusterCache: jobClusterCache{
					clusterForJob: map[string]string{
						"pull-ci-openshift-ci-tools-main-unit": "build01",
					},
					lastCleared: time.Now(),
				},
			}
			prowJob, err := s.generateProwJob(tc.jobRun)
			if diff := cmp.Diff(err, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("eror doesn't match expected, (-want +got):\n %v", err)
			}

			if err == nil {
				defaultProwJobFields(prowJob)
				testhelper.CompareWithFixture(t, prowJob)
			}
		})
	}
}

func TestDetermineProwJobName(t *testing.T) {
	ciTools := github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"}
	oc := github.Repo{Owner: github.User{Login: "openshift"}, Name: "oc"}
	cvo := github.Repo{Owner: github.User{Login: "openshift"}, Name: "cluster-version-operator"}
	installer := github.Repo{Owner: github.User{Login: "openshift"}, Name: "installer"}
	testCases := []struct {
		name     string
		jobRun   jobRun
		expected string
	}{
		{
			name: "additional PR from the repo containing the test",
			jobRun: jobRun{
				JobMetadata:   api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "ci-tools", Branch: "main"}, Test: "unit"},
				OriginPR:      github.PullRequest{Base: github.PullRequestBranch{Repo: ciTools}, Number: 999},
				AdditionalPRs: []github.PullRequest{{Base: github.PullRequestBranch{Repo: ciTools, Ref: "main"}, Number: 123}},
			},
			expected: "multi-pr-openshift-ci-tools-999-openshift-ci-tools-123-unit",
		},
		{
			name: "PR-free request for a test in the origin repo",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "ci-tools", Branch: "main", Variant: "variant"}, Test: "unit"},
				OriginPR:    github.PullRequest{Base: github.PullRequestBranch{Repo: ciTools}, Number: 999},
			},
			expected: "multi-pr-openshift-ci-tools-999-unit",
		},
		{
			name: "only an additional PR is from the repo containing the test",
			jobRun: jobRun{
				JobMetadata:   api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "cluster-version-operator", Branch: "main"}, Test: "e2e"},
				OriginPR:      github.PullRequest{Base: github.PullRequestBranch{Repo: oc, Ref: "main"}, Number: 2170},
				AdditionalPRs: []github.PullRequest{{Base: github.PullRequestBranch{Repo: cvo, Ref: "main"}, Number: 1}},
			},
			expected: "multi-pr-openshift-oc-2170-openshift-cluster-version-operator-1-e2e",
		},
		{
			name: "PR-free request for a test in a different repo includes the target",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "cluster-version-operator", Branch: "main"}, Test: "e2e"},
				OriginPR:    github.PullRequest{Base: github.PullRequestBranch{Repo: oc, Ref: "main"}, Number: 2170},
			},
			expected: "multi-pr-openshift-oc-2170-openshift-cluster-version-operator-main-e2e",
		},
		{
			name: "PR-free request for a variant test in a different repo includes the variant",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "cluster-version-operator", Branch: "main", Variant: "okd"}, Test: "e2e"},
				OriginPR:    github.PullRequest{Base: github.PullRequestBranch{Repo: oc, Ref: "main"}, Number: 2170},
			},
			expected: "multi-pr-openshift-oc-2170-openshift-cluster-version-operator-main-okd-e2e",
		},
		{
			name: "no PR from the repo containing the test includes the target after the additional PRs",
			jobRun: jobRun{
				JobMetadata:   api.MetadataWithTest{Metadata: api.Metadata{Org: "openshift", Repo: "cluster-version-operator", Branch: "main"}, Test: "e2e"},
				OriginPR:      github.PullRequest{Base: github.PullRequestBranch{Repo: oc, Ref: "main"}, Number: 2170},
				AdditionalPRs: []github.PullRequest{{Base: github.PullRequestBranch{Repo: installer, Ref: "main"}, Number: 5}},
			},
			expected: "multi-pr-openshift-oc-2170-openshift-installer-5-openshift-cluster-version-operator-main-e2e",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.expected, determineProwJobName(tc.jobRun)); diff != "" {
				t.Errorf("unexpected job name (-want +got):\n%s", diff)
			}
		})
	}
}

func addCIOpConfigsFromPRs(configs map[api.Metadata]*api.ReleaseBuildConfiguration, prs []github.PullRequest) {
	for _, pr := range prs {
		k := api.Metadata{Org: pr.Base.Repo.Owner.Login, Repo: pr.Base.Repo.Name, Branch: pr.Base.Ref}
		if _, ok := configs[k]; !ok {
			configs[k] = &api.ReleaseBuildConfiguration{}
		}
	}
}

var (
	zeroTime = metav1.NewTime(time.Unix(0, 0))
)

func defaultProwJobFields(prowJob *prowv1.ProwJob) {
	prowJob.Status.StartTime = zeroTime
	if prowJob.Status.CompletionTime != nil {
		prowJob.Status.CompletionTime = &zeroTime
	}
	prowJob.Name = "some-uuid"
}

func TestCreateRefsForPullRequests(t *testing.T) {
	ciOpConfigResolver := &fakeCIOpConfigResolver{
		configs: map[api.Metadata]*api.ReleaseBuildConfiguration{
			{Org: "openshift", Repo: "ci-tools", Branch: "main"}:    {CanonicalGoRepository: ptr.To("ci-tools-main-path-alias")},
			{Org: "openshift", Repo: "ci-tools", Branch: "dev"}:     {CanonicalGoRepository: ptr.To("ci-tools-dev-path-alias")},
			{Org: "openshift", Repo: "apiserver", Branch: "master"}: {},
		},
	}

	sortRefsOpt := cmpopts.SortSlices(func(a, b prowv1.Refs) bool {
		aKey := a.Org + a.Repo + a.BaseRef + a.PathAlias + a.BaseSHA
		bKey := b.Org + b.Repo + b.BaseRef + b.PathAlias + b.BaseSHA
		return strings.Compare(aKey, bKey) == -1
	})

	for _, tc := range []struct {
		name     string
		prs      []github.PullRequest
		wantRefs []prowv1.Refs
	}{
		{
			name: "Refs from heterogeneous PRs",
			prs: []github.PullRequest{
				{
					Number: 1,
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{
								Login: "openshift",
							},
							Name: "ci-tools",
						},
						SHA: "pr-1-base-sha",
						Ref: "main",
					},
					User: github.User{
						Login: "user-a",
					},
					Head: github.PullRequestBranch{
						SHA: "pr-1-head-sha",
					},
					Title: "pr-1",
				},
				{
					Number: 2,
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{
								Login: "openshift",
							},
							Name: "ci-tools",
						},
						SHA: "pr-2-base-sha",
						Ref: "dev",
					},
					User: github.User{
						Login: "user-b",
					},
					Head: github.PullRequestBranch{
						SHA: "pr-2-head-sha",
					},
					Title: "pr-2",
				},
				{
					Number: 3,
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{
								Login: "openshift",
							},
							Name: "apiserver",
						},
						SHA: "pr-3-base-sha",
						Ref: "master",
					},
					User: github.User{
						Login: "user-c",
					},
					Head: github.PullRequestBranch{
						SHA: "pr-3-head-sha",
					},
					Title: "pr-3",
				},
			},
			wantRefs: []prowv1.Refs{
				{
					Org:       "openshift",
					Repo:      "ci-tools",
					BaseRef:   "main",
					PathAlias: "ci-tools-main-path-alias",
					BaseSHA:   "heads/main",
					Pulls: []prowv1.Pull{{
						Number: 1,
						Author: "user-a",
						SHA:    "pr-1-head-sha",
						Title:  "pr-1",
					}},
				},
				{
					Org:       "openshift",
					Repo:      "ci-tools",
					BaseRef:   "dev",
					PathAlias: "ci-tools-dev-path-alias",
					BaseSHA:   "heads/dev",
					Pulls: []prowv1.Pull{{
						Number: 2,
						Author: "user-b",
						SHA:    "pr-2-head-sha",
						Title:  "pr-2",
					}},
				},
				{
					Org:       "openshift",
					Repo:      "apiserver",
					BaseRef:   "master",
					PathAlias: "",
					BaseSHA:   "heads/master",
					Pulls: []prowv1.Pull{{
						Number: 3,
						Author: "user-c",
						SHA:    "pr-3-head-sha",
						Title:  "pr-3",
					}},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotRefs, err := createRefsForPullRequests(tc.prs, ciOpConfigResolver, &fakeGithubClient{})
			if err != nil {
				t.Errorf("unexpected err: %s", err)
			}
			if diff := cmp.Diff(tc.wantRefs, gotRefs, sortRefsOpt); diff != "" {
				t.Errorf("unexpected refs: %s", diff)
			}
		})
	}
}
