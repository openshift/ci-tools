package main

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

type orgrepopr struct {
	org, repo string
	pr        int
}

type fakeClient struct {
	commits      map[orgrepopr][]github.RepositoryCommit
	commitErrors map[orgrepopr]error

	prs      map[orgrepopr]*github.PullRequest
	prErrors map[orgrepopr]error

	comments map[orgrepopr][]string

	labels map[orgrepopr][]string
}

func (c *fakeClient) ListPullRequestCommits(org, repo string, number int) ([]github.RepositoryCommit, error) {
	orp := orgrepopr{org: org, repo: repo, pr: number}
	if err, exist := c.commitErrors[orp]; exist && err != nil {
		return nil, err
	}

	if data, exist := c.commits[orp]; exist {
		return data, nil
	} else {
		return nil, errors.New("no commits configured for this PR")
	}
}

func (c *fakeClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	orp := orgrepopr{org: org, repo: repo, pr: number}
	if err, exist := c.prErrors[orp]; exist && err != nil {
		return nil, err
	}
	if data, exist := c.prs[orp]; exist {
		return data, nil
	} else {
		return nil, errors.New("no data configured for this PR")
	}
}

func (c *fakeClient) CreateComment(owner, repo string, number int, comment string) error {
	orp := orgrepopr{org: owner, repo: repo, pr: number}
	c.comments[orp] = append(c.comments[orp], comment)
	return nil
}

func (c *fakeClient) AddLabel(org, repo string, number int, label string) error {
	orp := orgrepopr{org: org, repo: repo, pr: number}
	c.labels[orp] = append(c.labels[orp], label)
	return nil
}

func (c *fakeClient) RemoveLabel(org, repo string, number int, label string) error {
	orp := orgrepopr{org: org, repo: repo, pr: number}
	var updated []string
	for _, old := range c.labels[orp] {
		if old != label {
			updated = append(updated, old)
		}
	}
	c.labels[orp] = updated
	return nil
}

func TestHandle(t *testing.T) {
	var testCases = []struct {
		name             string
		config           Config
		requested        bool
		commits          []github.RepositoryCommit
		commitError      error
		prs              map[orgrepopr]*github.PullRequest
		prErrors         map[orgrepopr]error
		labels           []string
		expectedLabels   []string
		expectedComments []string
	}{
		{
			name:             "no config",
			requested:        true,
			config:           Config{Repositories: map[string]string{}},
			expectedLabels:   []string{unvalidatedBackportsLabel},
			expectedComments: []string{"@author: no upstream repository is configured for validating backports for this repository."},
		},
		{
			name:             "no but not requested explicitly",
			requested:        false,
			config:           Config{Repositories: map[string]string{}},
			expectedComments: []string{},
		},
		{
			name:   "valid upstreams",
			config: Config{Repositories: map[string]string{"org/repo": "upstream/repo"}},
			commits: []github.RepositoryCommit{
				{SHA: "123456789", Commit: github.GitCommit{Message: "UPSTREAM: 1: whoa"}},
				{SHA: "456789abc", Commit: github.GitCommit{Message: "UPSTREAM: 2: whoa"}},
				{SHA: "789abcdef", Commit: github.GitCommit{Message: "UPSTREAM: 3: whoa"}},
			},
			prs: map[orgrepopr]*github.PullRequest{
				{org: "upstream", repo: "repo", pr: 1}: {Merged: true},
				{org: "upstream", repo: "repo", pr: 2}: {Merged: true},
				{org: "upstream", repo: "repo", pr: 3}: {Merged: true},
			},
			labels:         []string{unvalidatedBackportsLabel},
			expectedLabels: []string{validatedBackportsLabel},
			expectedComments: []string{`@author: the contents of this pull request could be automatically validated.

The following commits are valid:
 - [1234567|UPSTREAM: 1: whoa](https://github.com/org/repo/commit/123456789): the upstream PR [upstream/repo#1](https://redirect.github.com/upstream/repo/pull/1) has merged
 - [456789a|UPSTREAM: 2: whoa](https://github.com/org/repo/commit/456789abc): the upstream PR [upstream/repo#2](https://redirect.github.com/upstream/repo/pull/2) has merged
 - [789abcd|UPSTREAM: 3: whoa](https://github.com/org/repo/commit/789abcdef): the upstream PR [upstream/repo#3](https://redirect.github.com/upstream/repo/pull/3) has merged

Comment <code>/validate-backports</code> to re-evaluate validity of the upstream PRs, for example when they are merged upstream.`},
		},
		{
			name:   "invalid upstreams",
			config: Config{Repositories: map[string]string{"org/repo": "upstream/repo"}},
			commits: []github.RepositoryCommit{
				{SHA: "123456789", Commit: github.GitCommit{Message: "UPSTREAM: 1: whoa"}},
				{SHA: "456789abc", Commit: github.GitCommit{Message: "UPSTREAM: 2: whoa"}},
				{SHA: "789abcdef", Commit: github.GitCommit{Message: "UPSTREAM: 3: whoa"}},
				{SHA: "abcdefghi", Commit: github.GitCommit{Message: "UPSTREAM: <carry>: whoa"}},
				{SHA: "defghijkl", Commit: github.GitCommit{Message: "UPSTREAM: 4: whoa\nmore\ndata\nto\nbe\nskipped"}},
			},
			prs: map[orgrepopr]*github.PullRequest{
				{org: "upstream", repo: "repo", pr: 1}: {Merged: true},
				{org: "upstream", repo: "repo", pr: 2}: {Merged: false},
			},
			prErrors: map[orgrepopr]error{
				{org: "upstream", repo: "repo", pr: 3}: errors.New("injected error"),
				{org: "upstream", repo: "repo", pr: 4}: github.NewNotFound(),
			},
			expectedLabels: []string{unvalidatedBackportsLabel},
			expectedComments: []string{`@author: the contents of this pull request could not be automatically validated.

The following commits are valid:
 - [1234567|UPSTREAM: 1: whoa](https://github.com/org/repo/commit/123456789): the upstream PR [upstream/repo#1](https://redirect.github.com/upstream/repo/pull/1) has merged

The following commits could not be validated and must be approved by a top-level approver:
 - [456789a|UPSTREAM: 2: whoa](https://github.com/org/repo/commit/456789abc): the upstream PR [upstream/repo#2](https://redirect.github.com/upstream/repo/pull/2) has not yet merged
 - [abcdefg|UPSTREAM: <carry>: whoa](https://github.com/org/repo/commit/abcdefghi): does not specify an upstream backport in the commit message
 - [defghij|UPSTREAM: 4: whoa](https://github.com/org/repo/commit/defghijkl): the upstream PR [upstream/repo#4](https://redirect.github.com/upstream/repo/pull/4) does not exist

The following commits could not be processed:
 - [789abcd|UPSTREAM: 3: whoa](https://github.com/org/repo/commit/789abcdef): failed to fetch upstream PR: injected error

Comment <code>/validate-backports</code> to re-evaluate validity of the upstream PRs, for example when they are merged upstream.`},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testCase := tc
			t.Parallel()
			orp := orgrepopr{
				org:  "org",
				repo: "repo",
				pr:   1,
			}
			client := &fakeClient{
				commits:      map[orgrepopr][]github.RepositoryCommit{orp: testCase.commits},
				commitErrors: map[orgrepopr]error{orp: testCase.commitError},
				prs:          testCase.prs,
				prErrors:     testCase.prErrors,
				comments:     map[orgrepopr][]string{orp: {}},
				labels:       map[orgrepopr][]string{orp: testCase.labels},
			}
			s := &server{
				config: func() *Config {
					return &testCase.config
				},
				ghc: client,
			}

			s.handle(logrus.WithField("testcase", testCase.name), "org", "repo", "author", 1, testCase.requested)

			if diff := cmp.Diff(testCase.expectedComments, client.comments[orp]); diff != "" {
				t.Errorf("%s: got incorrect comments: %v", testCase.name, diff)
			}

			if diff := cmp.Diff(testCase.expectedLabels, client.labels[orp]); diff != "" {
				t.Errorf("%s: got incorrect labels: %v", testCase.name, diff)
			}
		})
	}
}
