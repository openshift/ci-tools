package main

import (
	"context"
	"errors"
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
	True := true
	c := &Config{
		Retester: Retester{
			RetesterPolicy: RetesterPolicy{
				MaxRetestsForSha: 1, MaxRetestsForShaAndBase: 1, Enabled: &True,
			},
			Oranizations: map[string]Oranization{"openshift": {
				RetesterPolicy: RetesterPolicy{
					MaxRetestsForSha: 2, MaxRetestsForShaAndBase: 2, Enabled: &True,
				},
				Repos: map[string]Repo{
					"ci-docs": {RetesterPolicy: RetesterPolicy{Enabled: &True}},
					"ci-tools": {RetesterPolicy: RetesterPolicy{
						MaxRetestsForSha: 3, MaxRetestsForShaAndBase: 3, Enabled: &True,
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
			name:     "config",
			file:     "testdata/testconfig/config.yaml",
			expected: c,
		},
		{
			name:     "default",
			file:     "testdata/testconfig/default.yaml",
			expected: &Config{Retester: Retester{RetesterPolicy: RetesterPolicy{MaxRetestsForSha: 9, MaxRetestsForShaAndBase: 3}}},
		},
		{
			name:     "empty",
			file:     "testdata/testconfig/empty.yaml",
			expected: &Config{Retester: Retester{}},
		},
		{
			name:     "no-config",
			file:     "testdata/testconfig/no-config.yaml",
			expected: &Config{Retester: Retester{}},
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
	True := true
	False := false
	c := &Config{
		Retester: Retester{
			RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 3, MaxRetestsForSha: 9},
			Oranizations: map[string]Oranization{
				"openshift": {
					RetesterPolicy: RetesterPolicy{
						MaxRetestsForSha: 2, MaxRetestsForShaAndBase: 2, Enabled: &True,
					},
					Repos: map[string]Repo{
						"ci-docs": {RetesterPolicy: RetesterPolicy{}},
						"ci-tools": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 3, MaxRetestsForShaAndBase: 3, Enabled: &True,
						}},
						"repo-max": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 6, Enabled: &True,
						}},
						"repo": {RetesterPolicy: RetesterPolicy{Enabled: &False}},
					}},
				"no-openshift": {
					RetesterPolicy: RetesterPolicy{Enabled: &False},
					Repos: map[string]Repo{
						"ci-docs": {RetesterPolicy: RetesterPolicy{}},
						"true":    {RetesterPolicy: RetesterPolicy{Enabled: &True}},
						"ci-tools": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 4, MaxRetestsForShaAndBase: 4, Enabled: &True,
						}},
						"repo": {RetesterPolicy: RetesterPolicy{Enabled: &False}},
					}},
			},
		}}
	var num githubv4.Int = 123
	testCases := []struct {
		name          string
		pr            tide.PullRequest
		config        *Config
		expected      RetesterPolicy
		expectedError error
	}{
		{
			name: "enabled repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-tools", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
			},
			config:   c,
			expected: RetesterPolicy{3, 3, &True},
		},
		{
			name: "enabled repo with one max retest value and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "repo-max", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
			},
			config:   c,
			expected: RetesterPolicy{2, 6, &True},
		},
		{
			name: "enabled repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "no-openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-tools", Owner: struct{ Login githubv4.String }{Login: "no-openshift"}},
			},
			config:   c,
			expected: RetesterPolicy{4, 4, &True},
		},
		{
			name: "disabled repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "repo", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
			},
			config:        c,
			expected:      RetesterPolicy{},
			expectedError: nil,
		},
		{
			name: "disabled repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "no-openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "repo", Owner: struct{ Login githubv4.String }{Login: "no-openshift"}},
			},
			config:        c,
			expected:      RetesterPolicy{},
			expectedError: nil,
		},
		{
			name: "not configured repo and enabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-docs", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
			},
			config:   c,
			expected: RetesterPolicy{2, 2, &True},
		},
		{
			name: "not configured repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "no-openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-docs", Owner: struct{ Login githubv4.String }{Login: "no-openshift"}},
			},
			config:        c,
			expected:      RetesterPolicy{},
			expectedError: nil,
		},
		{
			name: "configured repo and disabled org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "no-openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "true", Owner: struct{ Login githubv4.String }{Login: "no-openshift"}},
			},
			config:   c,
			expected: RetesterPolicy{3, 9, &True},
		},
		{
			name: "not configured repo and not configured org",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "org"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-docs", Owner: struct{ Login githubv4.String }{Login: "org"}},
			},
			config:        c,
			expected:      RetesterPolicy{},
			expectedError: nil,
		},
		{
			name: "Empty config",
			pr: tide.PullRequest{
				Number: num,
				Author: struct{ Login githubv4.String }{Login: "openshift"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "ci-tools", Owner: struct{ Login githubv4.String }{Login: "openshift"}},
			},
			config:        &Config{Retester{}},
			expected:      RetesterPolicy{},
			expectedError: nil,
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

func TestValidatePolicies(t *testing.T) {
	True := true
	False := false
	testCases := []struct {
		name     string
		policy   RetesterPolicy
		expected []error
	}{
		{
			name:     "basic case",
			policy:   RetesterPolicy{3, 9, &True},
			expected: nil,
		},
		{
			name:   "empty",
			policy: RetesterPolicy{},
			expected: []error{
				errors.New("policy is not enabled by default")},
		},
		{
			name:     "disable",
			policy:   RetesterPolicy{-1, -1, &False},
			expected: nil,
		},
		{
			name:   "negative",
			policy: RetesterPolicy{-1, -1, &True},
			expected: []error{
				errors.New("max_retest_for_sha has invalid value: -1"),
				errors.New("max_retests_for_sha_and_base has invalid value: -1")},
		},
		{
			name:     "lower",
			policy:   RetesterPolicy{9, 3, &True},
			expected: []error{errors.New("max_retest_for_sha value can't be lower than max_retests_for_sha_and_base value: 3 < 9")},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := validatePolicies(tc.policy)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestRetestOrBackoff(t *testing.T) {
	True := true
	config := &Config{Retester: Retester{
		RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 3, MaxRetestsForSha: 9}, Oranizations: map[string]Oranization{
			"org": {RetesterPolicy: RetesterPolicy{Enabled: &True}},
		},
	}}
	ghc := &MyFakeClient{fakegithub.NewFakeClient()}
	var num githubv4.Int = 123
	var num2 githubv4.Int = 321
	pr123 := github.PullRequest{}
	pr321 := github.PullRequest{}
	ghc.PullRequests = map[int]*github.PullRequest{123: &pr123, 321: &pr321}
	logger := logrus.NewEntry(
		logrus.StandardLogger())

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
				Author: struct{ Login githubv4.String }{Login: "org"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "repo", Owner: struct{ Login githubv4.String }{Login: "org"}},
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
				Author: struct{ Login githubv4.String }{Login: "failed test"},
				Repository: struct {
					Name          githubv4.String
					NameWithOwner githubv4.String
					Owner         struct{ Login githubv4.String }
				}{Name: "repo", Owner: struct{ Login githubv4.String }{Login: "failed test"}},
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
	True := true
	False := false
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
					RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 1, MaxRetestsForSha: 1, Enabled: &True}, Oranizations: map[string]Oranization{
						"openshift": {RetesterPolicy: RetesterPolicy{Enabled: &False},
							Repos: map[string]Repo{"ci-tools": {RetesterPolicy: RetesterPolicy{Enabled: &True}}},
						},
						"org-a": {RetesterPolicy: RetesterPolicy{Enabled: &True}},
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
