package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	prowflagutil "k8s.io/test-infra/prow/flagutil"
	github "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/github/fakegithub"
	"k8s.io/test-infra/prow/tide"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

type MyFakeClient struct {
	*fakegithub.FakeClient
}

func (f *MyFakeClient) QueryWithGitHubAppsSupport(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error {
	return nil
}

func (f *MyFakeClient) GetRef(owner, repo, ref string) (string, error) {
	if owner == "failed test" {
		return "", fmt.Errorf("failed")
	}
	return "abcde", nil
}

func TestRetestOrBackoff(t *testing.T) {
	ghc := &MyFakeClient{fakegithub.NewFakeClient()}
	var name githubv4.String = "repo"
	var notEnabledRepo githubv4.String = "other-repo"
	var owner githubv4.String = "org"
	var fail githubv4.String = "failed test"
	var num githubv4.Int = 123
	var num2 githubv4.Int = 321
	pr123 := github.PullRequest{}
	pr321 := github.PullRequest{}
	ghc.PullRequests = map[int]*github.PullRequest{123: &pr123, 321: &pr321}
	logger := logrus.NewEntry(logrus.StandardLogger())

	enableOnRepos := prowflagutil.NewStrings("org/repo")

	testCases := []struct {
		name          string
		pr            tide.PullRequest
		c             *retestController
		expected      string
		expectedError error
	}{
		{
			name: "basic case",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: owner},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: name, Owner: struct{ Login githubv4.String }{Login: owner}},
			},
			c: &retestController{
				ghClient:       ghc,
				logger:         logger,
				backoff:        &backoffCache{cache: map[string]*PullRequest{}, logger: logger},
				commentOnRepos: enableOnRepos.StringSet(),
			},
			expected: "/retest-required\n\nRemaining retests: 2 against base HEAD abcde and 8 for PR HEAD  in total\n",
		},
		{
			name: "no comment",
			pr: tide.PullRequest{
				Number: num2,
				Author: struct{ Login githubv4.String }{Login: owner},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: notEnabledRepo, Owner: struct{ Login githubv4.String }{Login: owner}},
			},
			c: &retestController{
				ghClient:       ghc,
				logger:         logger,
				backoff:        &backoffCache{cache: map[string]*PullRequest{}, logger: logger},
				commentOnRepos: enableOnRepos.StringSet(),
			},
			expected: "",
		},
		{
			name: "failed test",
			pr: tide.PullRequest{
				Number: num2,
				Author: struct{ Login githubv4.String }{Login: fail},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: notEnabledRepo, Owner: struct{ Login githubv4.String }{Login: fail}},
			},
			c: &retestController{
				ghClient:       ghc,
				logger:         logger,
				backoff:        &backoffCache{cache: map[string]*PullRequest{}, logger: logger},
				commentOnRepos: enableOnRepos.StringSet(),
			},
			expected:      "",
			expectedError: fmt.Errorf("failed"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.retestOrBackoff(tc.pr)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expectedError == nil {
				actual := ""
				if len(ghc.IssueComments[int(tc.pr.Number)]) != 0 {
					actual = ghc.IssueComments[int(tc.pr.Number)][0].Body
				}
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}
