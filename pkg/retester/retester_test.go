package retester

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/prow/github"
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

var (
	True  = true
	False = false
)

func TestLoadConfig(t *testing.T) {
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

	configOpenShift := &Config{
		Retester: Retester{
			RetesterPolicy: RetesterPolicy{
				MaxRetestsForSha: 9, MaxRetestsForShaAndBase: 3,
			},
			Oranizations: map[string]Oranization{"openshift": {
				RetesterPolicy: RetesterPolicy{
					Enabled: &True,
				},
			},

				"openshift-knative": {
					RetesterPolicy: RetesterPolicy{
						Enabled: &True,
					},
				},
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
			name:     "config",
			file:     "testdata/testconfig/openshift-config.yaml",
			expected: configOpenShift,
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
			actual, err := LoadConfig(tc.file)
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
			RetesterPolicy: RetesterPolicy{MaxRetestsForShaAndBase: 3, MaxRetestsForSha: 9},
			Oranizations: map[string]Oranization{
				"openshift": {
					RetesterPolicy: RetesterPolicy{
						MaxRetestsForSha: 2, MaxRetestsForShaAndBase: 2, Enabled: &True,
					},
					Repos: map[string]Repo{
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
						"true": {RetesterPolicy: RetesterPolicy{Enabled: &True}},
						"ci-tools": {RetesterPolicy: RetesterPolicy{
							MaxRetestsForSha: 4, MaxRetestsForShaAndBase: 4, Enabled: &True,
						}},
						"repo": {RetesterPolicy: RetesterPolicy{Enabled: &False}},
					}},
			},
		}}
	testCases := []struct {
		name          string
		org           string
		repo          string
		config        *Config
		expected      RetesterPolicy
		expectedError error
	}{
		{
			name:     "enabled repo and enabled org",
			org:      "openshift",
			repo:     "ci-tools",
			config:   c,
			expected: RetesterPolicy{3, 3, &True},
		},
		{
			name:     "enabled repo with one max retest value and enabled org",
			org:      "openshift",
			repo:     "repo-max",
			config:   c,
			expected: RetesterPolicy{2, 6, &True},
		},
		{
			name:     "enabled repo and disabled org",
			org:      "no-openshift",
			repo:     "ci-tools",
			config:   c,
			expected: RetesterPolicy{4, 4, &True},
		},
		{
			name:   "disabled repo and enabled org",
			org:    "openshift",
			repo:   "repo",
			config: c,
		},
		{
			name:     "not configured repo and enabled org",
			org:      "openshift",
			repo:     "ci-docs",
			config:   c,
			expected: RetesterPolicy{2, 2, &True},
		},
		{
			name:   "not configured repo and disabled org",
			org:    "no-openshifft",
			repo:   "ci-docs",
			config: c,
		},
		{
			name:     "configured repo and disabled org",
			org:      "no-openshift",
			repo:     "true",
			config:   c,
			expected: RetesterPolicy{3, 9, &True},
		},
		{
			name:   "not configured repo and not configured org",
			org:    "org",
			repo:   "ci-docs",
			config: c,
		},
		{
			name:   "Empty config",
			org:    "openshift",
			repo:   "ci-tools",
			config: &Config{Retester{}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := tc.config.GetRetesterPolicy(tc.org, tc.repo)
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

	testCases := []struct {
		name     string
		policy   RetesterPolicy
		expected []error
	}{
		{
			name:   "basic case",
			policy: RetesterPolicy{3, 9, &True},
		},
		{
			name: "empty policy is valid",
		},
		{
			name:   "disable",
			policy: RetesterPolicy{-1, -1, &False},
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
		c             *RetestController
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
			c: &RetestController{
				ghClient: ghc,
				logger:   logger,
				backoff:  &backoffCache{cache: map[string]*pullRequest{}, logger: logger},
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
			c: &RetestController{
				ghClient: ghc,
				logger:   logger,
				backoff:  &backoffCache{cache: map[string]*pullRequest{}, logger: logger},
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
		c          *RetestController
		candidates map[string]tide.PullRequest
		expected   map[string]tide.PullRequest
	}{
		{
			name: "basic case",
			c: &RetestController{
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

func TestLoadFromDisk(t *testing.T) {
	logger := logrus.NewEntry(logrus.StandardLogger())
	testCases := []struct {
		name           string
		file           string
		cacheRecordAge string
		bytes          []byte
		perm           fs.FileMode
		expectedError  error
	}{
		{
			name:           "basic case",
			file:           "backoff.cache",
			cacheRecordAge: "24h",
			bytes: []byte(`pr:
  last_considered_time: "2200-01-01T00:00:00Z"`),
			perm:          0644,
			expectedError: nil,
		},
		{
			name:           "empty file",
			file:           "",
			cacheRecordAge: "24h",
			perm:           0644,
			expectedError:  nil,
		},
		{
			name:           "file no exist",
			file:           "no-exist.cache",
			cacheRecordAge: "24h",
			perm:           0644,
			expectedError:  nil,
		},
		{
			name:           "file no read perm",
			file:           "backoff.cache",
			cacheRecordAge: "24h",
			perm:           0000,
			expectedError:  errors.New("failed to read file backoff.cache: open backoff.cache: permission denied"),
		},
		{
			name:           "wrong formatting",
			file:           "backoff.cache",
			cacheRecordAge: "24h",
			bytes: []byte(`wrong:
			formating"`),
			perm:          0644,
			expectedError: errors.New("failed to unmarshal: error converting YAML to JSON: yaml: line 2: found character that cannot start any token"),
		},
		{
			name:           "old case",
			file:           "backoff.cache",
			cacheRecordAge: "24h",
			bytes: []byte(`pr:
  last_considered_time: "1970-01-01T00:00:00Z"`),
			perm:          0644,
			expectedError: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				tmp := "backoff.cache"
				err := os.Remove(tmp)
				if err != nil {
					fmt.Errorf("failed to write file %s: %w", tmp, err)
				}
			}()
			tmp := "backoff.cache"
			if err := ioutil.WriteFile(tmp, tc.bytes, tc.perm); err != nil {
				fmt.Errorf("failed to write file %s: %w", tmp, err)
			}
			cacheRecordAge, _ := time.ParseDuration(tc.cacheRecordAge)
			backoff := &backoffCache{file: tc.file, cacheRecordAge: cacheRecordAge, logger: logger}
			err := backoff.loadFromDisk()
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
		})
	}
}

func TestSaveToDisk(t *testing.T) {
	logger := logrus.NewEntry(logrus.StandardLogger())
	testCases := []struct {
		name           string
		cache          map[string]*pullRequest
		file           string
		cacheRecordAge string
		expectedError  error
	}{
		{
			name:           "basic case",
			cache:          map[string]*pullRequest{"pr": {LastConsideredTime: v1.Now()}},
			file:           "backoff.cache",
			cacheRecordAge: "24h",
			expectedError:  nil,
		},
		{
			name:           "empty file",
			cache:          map[string]*pullRequest{},
			file:           "",
			cacheRecordAge: "24h",
			expectedError:  nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				tmp := "backoff.cache"
				err := os.Remove(tmp)
				if err != nil {
					fmt.Errorf("failed to write file %s: %w", tmp, err)
				}
			}()
			cacheRecordAge, _ := time.ParseDuration(tc.cacheRecordAge)
			backoff := &backoffCache{cache: tc.cache, file: tc.file, cacheRecordAge: cacheRecordAge, logger: logger}
			err := backoff.saveToDisk()
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
		})
	}
}
