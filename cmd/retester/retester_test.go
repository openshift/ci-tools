package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

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

func TestLoadConfig(t *testing.T) {
	c := &Config{
		Retester: Retester{
			RetesterPolicy: RetesterPolicy{
				MaxRetestsForSha: 1, MaxRetestsForShaAndBase: 1,
			},
			Oranizations: map[string]Oranization{"openshift": {
				RetesterPolicy: RetesterPolicy{
					MaxRetestsForSha: 2, MaxRetestsForShaAndBase: 2, Enabled: true,
				},
				Repos: map[string]Repo{
					"ci-docs": {RetesterPolicy: RetesterPolicy{Enabled: true}},
					"ci-tools": {RetesterPolicy: RetesterPolicy{
						MaxRetestsForSha: 3, MaxRetestsForShaAndBase: 3, Enabled: true,
					}},
				}},
			},
		}}
	testCases := []struct {
		name          string
		file          string
		expected      *Config
		expectedError error
	}{
		{
			name:     "basic case",
			file:     "testdata/testconfig/config.yaml",
			expected: c,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := loadConfig(tc.file)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expectedError == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func TestGetRetesterPolicy(t *testing.T) {
	c := &Config{
		Retester: Retester{
			RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 1, MaxRetestsForSha: 1},
			Oranizations: map[string]Oranization{
				"openshift": {
					RetesterPolicy: RetesterPolicy{
						MaxRetestsForSha: 2, MaxRetestsForShaAndBase: 2, Enabled: true,
					},
					Repos: map[string]Repo{
						"ci-docs": {RetesterPolicy: RetesterPolicy{Enabled: true}},
						"ci-tools": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 3, MaxRetestsForShaAndBase: 3, Enabled: true,
						}},
						"repo": {RetesterPolicy: RetesterPolicy{Enabled: false}},
					}},
				"no-openshift": {
					RetesterPolicy: RetesterPolicy{Enabled: false},
					Repos: map[string]Repo{
						"ci-docs": {RetesterPolicy: RetesterPolicy{Enabled: true}},
						"ci-tools": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 4, MaxRetestsForShaAndBase: 4, Enabled: true,
						}},
						"repo": {RetesterPolicy: RetesterPolicy{Enabled: false}},
					}},
			},
		}}
	var confRepo githubv4.String = "ci-tools"
	var nonConfEnableRepo githubv4.String = "ci-docs"
	var nonConfDisableRepo githubv4.String = "repo"
	var confEnableOrg githubv4.String = "openshift"
	var confDisableOrg githubv4.String = "no-openshift"
	var nonConfOrg githubv4.String = "org"
	var num githubv4.Int = 123
	testCases := []struct {
		name          string
		pr            tide.PullRequest
		config        *Config
		expected      *RetesterPolicy
		expectedError error
	}{
		{
			name: "enabled repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: confRepo, Owner: struct{ Login githubv4.String }{Login: confEnableOrg}},
			},
			config:   c,
			expected: &RetesterPolicy{3, 3, true},
		},
		{
			name: "enabled repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: confRepo, Owner: struct{ Login githubv4.String }{Login: confDisableOrg}},
			},
			config:   c,
			expected: &RetesterPolicy{4, 4, true},
		},
		{
			name: "enabled repo and not configured org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: confRepo, Owner: struct{ Login githubv4.String }{Login: nonConfOrg}},
			},
			config:        c,
			expected:      &RetesterPolicy{Enabled: false},
			expectedError: fmt.Errorf("not configured org"),
		},
		{
			name: "disabled repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfDisableRepo, Owner: struct{ Login githubv4.String }{Login: confEnableOrg}},
			},
			config:        c,
			expected:      &RetesterPolicy{Enabled: false},
			expectedError: fmt.Errorf("repo is disabled"),
		},
		{
			name: "disabled repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfDisableRepo, Owner: struct{ Login githubv4.String }{Login: confDisableOrg}},
			},
			config:        c,
			expected:      &RetesterPolicy{Enabled: false},
			expectedError: fmt.Errorf("repo is disabled"),
		},
		{
			name: "disabled repo and not configured org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfDisableRepo, Owner: struct{ Login githubv4.String }{Login: nonConfOrg}},
			},
			config:        c,
			expected:      &RetesterPolicy{Enabled: false},
			expectedError: fmt.Errorf("not configured org"),
		},
		{
			name: "enabled not configured repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfEnableRepo, Owner: struct{ Login githubv4.String }{Login: confEnableOrg}},
			},
			config:   c,
			expected: &RetesterPolicy{2, 2, true},
		},
		{
			name: "enabled not configured repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfEnableRepo, Owner: struct{ Login githubv4.String }{Login: confDisableOrg}},
			},
			config:   c,
			expected: &RetesterPolicy{1, 1, true},
		},
		{
			name: "enabled not configured repo and not configured org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: confEnableOrg},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: nonConfEnableRepo, Owner: struct{ Login githubv4.String }{Login: nonConfOrg}},
			},
			config:        c,
			expected:      &RetesterPolicy{Enabled: false},
			expectedError: fmt.Errorf("not configured org"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			org := string(tc.pr.Repository.Owner.Login)
			repo := string(tc.pr.Repository.Name)
			actual, err := tc.config.GetRetesterPolicy(org, repo)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expectedError == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func TestRetestOrBackoff(t *testing.T) {
	config := &Config{Retester: Retester{
		RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 3, MaxRetestsForSha: 9}, Oranizations: map[string]Oranization{
			"org": {RetesterPolicy: RetesterPolicy{Enabled: true}},
		},
	}}
	ghc := &MyFakeClient{fakegithub.NewFakeClient()}
	var name githubv4.String = "repo"
	var owner githubv4.String = "org"
	var fail githubv4.String = "failed test"
	var num githubv4.Int = 123
	var num2 githubv4.Int = 321
	pr123 := github.PullRequest{}
	pr321 := github.PullRequest{}
	ghc.PullRequests = map[int]*github.PullRequest{123: &pr123, 321: &pr321}
	logger := logrus.NewEntry(logrus.StandardLogger())

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
				ghClient: ghc,
				logger:   logger,
				backoff:  &backoffCache{cache: map[string]*PullRequest{}, logger: logger},
				config:   config,
			},
			expected: "/retest-required\n\nRemaining retests: 2 against base HEAD abcde and 8 for PR HEAD  in total\n",
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
				}{Name: name, Owner: struct{ Login githubv4.String }{Login: fail}},
			},
			c: &retestController{
				ghClient: ghc,
				logger:   logger,
				backoff:  &backoffCache{cache: map[string]*PullRequest{}, logger: logger},
				config:   config,
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

func TestEnabledPRs(t *testing.T) {
	logger := logrus.NewEntry(logrus.StandardLogger())
	testCases := []struct {
		name       string
		c          *retestController
		candidates map[string]tide.PullRequest
		expected   map[string]tide.PullRequest
	}{
		{
			name: "basic case",
			c: &retestController{
				config: &Config{Retester: Retester{
					RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 1, MaxRetestsForSha: 1}, Oranizations: map[string]Oranization{
						"openshift": {RetesterPolicy: RetesterPolicy{Enabled: false},
							Repos: map[string]Repo{"ci-tools": {RetesterPolicy: RetesterPolicy{Enabled: true}}},
						},
						"org-a": {RetesterPolicy: RetesterPolicy{Enabled: true}},
					},
				}},
				logger: logger,
			},
			candidates: map[string]tide.PullRequest{
				"a": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "ci-tools", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
				},
				"b": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "some-tools", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
				},
				"c": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "some-tools", Owner: struct{ Login githubv4.String }{Login: "org-a"}},
				},
				"d": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "some-tools", Owner: struct{ Login githubv4.String }{Login: "org-b"}},
				},
			},
			expected: map[string]tide.PullRequest{
				"a": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "ci-tools", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
				},
				"c": {
					Number: 1,
					Repository: struct {
						Name          githubv4.String
						NameWithOwner githubv4.String
						Owner         struct{ Login githubv4.String }
					}{Name: "some-tools", Owner: struct{ Login githubv4.String }{Login: "org-a"}},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.c.enabledPRs(tc.candidates)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
