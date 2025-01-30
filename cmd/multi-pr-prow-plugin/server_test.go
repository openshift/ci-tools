package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
}

func (c fakeGithubClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	orgRepoNumber := fmt.Sprintf("%s/%s#%d", org, repo, number)
	return c.prs[orgRepoNumber], nil
}

func (c fakeGithubClient) CreateComment(owner, repo string, number int, comment string) error {
	//no-op
	return nil
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
	}{
		{
			name: "trigger a single multi-pr job run with additional PR from the same repo",
			issueComment: github.IssueCommentEvent{
				GUID: "guid",
				Repo: github.Repo{Owner: github.User{Login: "openshift"}, Name: "ci-tools"},
				Issue: github.Issue{
					Number:      999,
					PullRequest: &struct{}{},
				},
				Comment: github.IssueComment{
					Body: "/testwith openshift/ci-tools/master/unit openshift/ci-tools#123",
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
					Ref: "master",
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
					Body: "/testwith openshift/ci-tools/master/unit openshift/release#876",
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
					Ref: "master",
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
					Body: `/testwith openshift/ci-tools/master/unit openshift/ci-tools#123
/testwith openshift/ci-tools/master/e2e openshift/release#876
/testwith openshift/ci-tools/master/unit https://github.com/openshift/release/pull/876`,
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
					Ref: "master",
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
					Body: "/testwith openshift/ci-tools/master/unit openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876 openshift/release#876",
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
					Ref: "master",
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
					Body: "/testwith openshift/ci-tools/master/unit openshift/release#876",
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
					Ref: "master",
				},
			},
			kubeClient:  fakectrlruntimeclient.NewFakeClient(),
			expectedErr: errors.New("the user: not-trusted is not trusted to trigger tests"),
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
					Ref: "master",
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
						Ref: "master",
					},
					Number: 123,
				},
				"openshift/release#876": {
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "release",
						},
						Ref: "master",
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
						"pull-ci-openshift-ci-tools-master-unit": "build01",
						"pull-ci-openshift-ci-tools-master-e2e":  "build02",
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
				for _, prowJob := range prowJobs {
					defaultProwJobFields(prowJob)
				}
				testhelper.CompareWithFixture(t, prowJobs)
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
			comment: "/testwith openshift/ci-tools/master/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
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
						},
						Number: 123,
					},
				},
			}},
		},
		{
			name:    "trigger a single job including a variant",
			comment: "/testwith openshift/ci-tools/master/variant/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:     "openshift",
						Repo:    "ci-tools",
						Branch:  "master",
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
						},
						Number: 123,
					},
				},
			}},
		},
		{
			name: "trigger multiple jobs with an additional PR from the same repo",
			comment: `/testwith openshift/ci-tools/master/unit openshift/ci-tools#123
/testwith openshift/ci-tools/master/e2e https://github.com/openshift/ci-tools/pull/123`,
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expected: []jobRun{
				{
					JobMetadata: api.MetadataWithTest{
						Metadata: api.Metadata{
							Org:    "openshift",
							Repo:   "ci-tools",
							Branch: "master",
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
							Branch: "master",
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
							},
							Number: 123,
						},
					},
				},
			},
		},
		{
			name:    "trigger a single job with multiple additional PRs",
			comment: "/testwith openshift/ci-tools/master/unit openshift/ci-tools#123 openshift/release#876",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
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
			name:    "invalid format for additional PR",
			comment: "/testwith openshift/ci-tools/master/unit openshift/ci-tools/123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expectedError: errors.New("invalid format for additional PR: openshift/ci-tools/123"),
		},
		{
			name:    "invalid format for job",
			comment: "/testwith openshift/ci-tools/master/blaster/faster/unit openshift/ci-tools#123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expectedError: errors.New("requested job is invalid. needs to be formatted like: <org>/<repo>/<branch>/<variant?>/<job>. instead it was: openshift/ci-tools/master/blaster/faster/unit"),
		},
		{
			name:    "trigger a single job with an additional PR in the github url format",
			comment: "/testwith openshift/ci-tools/master/unit https://github.com/openshift/ci-tools/pull/123",
			originPR: github.PullRequest{
				Base: github.PullRequestBranch{
					Repo: github.Repo{
						Owner: github.User{Login: "openshift"},
						Name:  "ci-tools",
					},
				},
				Number: 999,
			},
			expected: []jobRun{{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
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
	configs map[api.Metadata]*api.ReleaseBuildConfiguration
}

func (r fakeCIOpConfigResolver) Config(m *api.Metadata) (*api.ReleaseBuildConfiguration, error) {
	if m == nil {
		return nil, fmt.Errorf("some error")
	}

	return r.configs[*m], nil
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
	if jobName == "pull-ci-openshift-ci-tools-master-missing" {
		return "", fmt.Errorf("job: %s not found", jobName)
	} else {
		return "build02", nil
	}
}

func TestGenerateProwJob(t *testing.T) {
	testCases := []struct {
		name          string
		jobRun        jobRun
		expectedError error
	}{
		{
			name: "additional PR from the same repo",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
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
							Ref: "master",
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
						Branch: "master",
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
							Ref: "master",
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
			name: "cluster not found for job",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
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
							Ref: "master",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
			expectedError: errors.New("could not determine cluster for job pull-ci-openshift-ci-tools-master-missing: job: pull-ci-openshift-ci-tools-master-missing not found"),
		},
		{
			name: "no ref for requested test included",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "release",
						Branch: "master",
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
							Ref: "master",
						},
						Number: 123,
						User:   github.User{Login: "other-dev"},
						Head:   github.PullRequestBranch{SHA: "SOME_SHA"},
					},
				},
			},
			expectedError: errors.New("No ref for requested test included in command. The org, repo, and branch containing the requested test need to be targeted by at least one of the included PRs."),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := server{
				ciOpConfigResolver: &fakeCIOpConfigResolver{
					configs: map[api.Metadata]*api.ReleaseBuildConfiguration{
						tc.jobRun.JobMetadata.Metadata: {
							Tests: []api.TestStepConfiguration{
								{
									As: tc.jobRun.JobMetadata.Test,
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
						"pull-ci-openshift-ci-tools-master-unit": "build01",
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
