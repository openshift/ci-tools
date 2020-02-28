package main

import (
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/repoowners"
)

func assertEqual(t *testing.T, actual, expected interface{}) {
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual differs from expected:\n%s", diff.ObjectReflectDiff(expected, actual))
	}
}

func TestResolveAliases(t *testing.T) {
	ra := RepoAliases{}
	ra["sig-alias"] = sets.NewString("bob", "carol")
	ra["cincinnati-reviewers"] = sets.NewString()

	testCases := []struct {
		description string
		given       httpResult
		expected    interface{}
	}{
		{
			description: "Simple Config case without initialized RequiredReviewers",
			given: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"david", "sig-alIas", "Alice"},
						Reviewers: []string{"adam", "sig-alias"},
					},
				},
				repoAliases: ra,
			},
			expected: SimpleConfig{
				Config: repoowners.Config{
					Approvers:         []string{"alice", "bob", "carol", "david"},
					Reviewers:         []string{"adam", "bob", "carol"},
					RequiredReviewers: []string{},
					Labels:            []string{},
				},
			},
		},
		{
			description: "Simple Config case with initialized RequiredReviewers",
			given: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers:         []string{"david", "sig-alias", "alice"},
						Reviewers:         []string{"adam", "sig-alias"},
						RequiredReviewers: []string{},
					},
				},
				repoAliases: ra,
			},
			expected: SimpleConfig{
				Config: repoowners.Config{
					Approvers:         []string{"alice", "bob", "carol", "david"},
					Reviewers:         []string{"adam", "bob", "carol"},
					RequiredReviewers: []string{},
					Labels:            []string{},
				},
			},
		},
		{
			description: "Simple Config case with empty set to an alias",
			given: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"david", "sig-alIas", "Alice"},
						Reviewers: []string{"cincinnati-reviewers"},
					},
				},
				repoAliases: ra,
			},
			expected: SimpleConfig{
				Config: repoowners.Config{
					Approvers:         []string{"alice", "bob", "carol", "david"},
					Reviewers:         []string{},
					RequiredReviewers: []string{},
					Labels:            []string{},
				},
			},
		},
		{
			description: "Full Config case",
			given: httpResult{
				repoAliases: ra,
				fullConfig: FullConfig{
					Filters: map[string]repoowners.Config{
						"abc": {
							Approvers: []string{"david", "sig-alias", "alice"},
							Reviewers: []string{"adam", "sig-alias"},
						},
					},
				},
			},
			expected: FullConfig{
				Filters: map[string]repoowners.Config{
					"abc": {
						Approvers:         []string{"alice", "bob", "carol", "david"},
						Reviewers:         []string{"adam", "bob", "carol"},
						RequiredReviewers: []string{},
						Labels:            []string{},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := tc.given.resolveOwnerAliases(noOpCleaner)
			assertEqual(t, result, tc.expected)
		})
	}
}

func TestGetTitle(t *testing.T) {
	expect := "Sync OWNERS files by autoowners job at Thu, 12 Sep 2019 14:56:10 EDT"
	result := getTitle("Sync OWNERS files", "Thu, 12 Sep 2019 14:56:10 EDT")

	if expect != result {
		t.Errorf("title '%s' differs from expected '%s'", result, expect)
	}
}

func TestGetBody(t *testing.T) {
	expect := `The OWNERS file has been synced for the following folder(s):

* config/openshift/origin
* jobs/org/repo

/cc @openshift/openshift-team-developer-productivity-test-platform
`
	result := getBody([]string{"config/openshift/origin", "jobs/org/repo"}, githubTeam)

	if expect != result {
		t.Errorf("body '%s' differs from expected '%s'", result, expect)
	}
}

func TestListUpdatedDirectoriesFromGitStatusOutput(t *testing.T) {
	output := ` M ci-operator/config/openshift/cincinnati/OWNERS
 M ci-operator/config/openshift/cluster-api-provider-aws/OWNERS
 M ci-operator/jobs/openshift/cluster-api-provider-openstack/OWNERS
`
	actual, err := listUpdatedDirectoriesFromGitStatusOutput(output)
	expected := []string{"config/openshift/cincinnati", "config/openshift/cluster-api-provider-aws", "jobs/openshift/cluster-api-provider-openstack"}
	if err != nil {
		t.Errorf("unexpected error occurred when listUpdatedDirectoriesFromGitStatusOutput")
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual differs from expected:\n%s", diff.ObjectReflectDiff(expected, actual))
	}
}

type fakeFileGetter struct {
	owners    []byte
	aliases   []byte
	someError error
	notFound  error
}

func (fg fakeFileGetter) GetFile(org, repo, filepath, commit string) ([]byte, error) {
	if org == "org1" && repo == "repo1" {
		if filepath == "OWNERS" {
			return fg.owners, nil
		}
		if filepath == "OWNERS_ALIASES" {
			return fg.aliases, nil
		}
	}
	if org == "org2" && repo == "repo2" {
		if filepath == "OWNERS" {
			return fg.owners, nil
		}
		if filepath == "OWNERS_ALIASES" {
			return nil, fg.notFound
		}
	}
	if org == "org3" && repo == "repo3" {
		if filepath == "OWNERS" {
			return nil, fg.notFound
		}
		if filepath == "OWNERS_ALIASES" {
			return nil, fg.notFound
		}
	}
	if org == "org4" && repo == "repo4" {
		if filepath == "OWNERS" {
			return nil, fg.notFound
		}
		if filepath == "OWNERS_ALIASES" {
			return fg.aliases, nil
		}
	}
	if org == "org5" && repo == "repo5" {
		if filepath == "OWNERS" {
			return nil, fg.someError
		}
		if filepath == "OWNERS_ALIASES" {
			return fg.aliases, nil
		}
	}
	if org == "org6" && repo == "repo6" {
		if filepath == "OWNERS" {
			return fg.owners, nil
		}
		if filepath == "OWNERS_ALIASES" {
			return nil, fg.someError
		}
	}
	return nil, nil
}

func TestGetOwnersHTTP(t *testing.T) {
	fakeOwners := []byte(`---
approvers:
- abc
- team-a
`)
	fakeOwnersAliases := []byte(`---
aliases:
  team-a:
  - aaa1
  - aaa2
`)
	someError := fmt.Errorf("some error")
	notFound := &github.FileNotFound{}

	ra := RepoAliases{}
	ra["team-a"] = sets.NewString("aaa1", "aaa2")

	fakeFileGetter := fakeFileGetter{
		owners:    fakeOwners,
		aliases:   fakeOwnersAliases,
		someError: someError,
		notFound:  notFound,
	}
	testCases := []struct {
		description        string
		given              orgRepo
		expectedHTTPResult httpResult
		expectedError      error
	}{
		{
			description: "both owner and alias exit",
			given: orgRepo{
				Organization: "org1",
				Repository:   "repo1",
			},
			expectedHTTPResult: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"abc", "team-a"},
					},
				},
				repoAliases:      ra,
				ownersFileExists: true,
			},
			expectedError: nil,
		},
		{
			description: "owner exists and alias not",
			given: orgRepo{
				Organization: "org2",
				Repository:   "repo2",
			},
			expectedHTTPResult: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"abc", "team-a"},
					},
				},
				ownersFileExists: true,
			},
			expectedError: nil,
		},
		{
			description: "neither owner nor alias exists",
			given: orgRepo{
				Organization: "org3",
				Repository:   "repo3",
			},
			expectedHTTPResult: httpResult{},
			expectedError:      nil,
		},
		{
			description: "owner does not exist and alias does",
			given: orgRepo{
				Organization: "org4",
				Repository:   "repo4",
			},
			expectedHTTPResult: httpResult{
				repoAliases: ra,
			},
			expectedError: nil,
		},
		{
			description: "owner is with normal error",
			given: orgRepo{
				Organization: "org5",
				Repository:   "repo5",
			},
			expectedHTTPResult: httpResult{},
			expectedError:      someError,
		},
		{
			description: "alias is with normal error",
			given: orgRepo{
				Organization: "org6",
				Repository:   "repo6",
			},
			expectedHTTPResult: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"abc", "team-a"},
					},
				},
				ownersFileExists: true,
			},
			expectedError: someError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := getOwnersHTTP(fakeFileGetter, tc.given)
			assertEqual(t, actual, tc.expectedHTTPResult)
			assertEqual(t, err, tc.expectedError)
		})
	}
}

func TestResolveOwnerAliasesCleans(t *testing.T) {
	cleaner := func(_ []string) []string {
		return []string{"hans"}
	}
	testCases := []struct {
		name           string
		in             httpResult
		expectedResult interface{}
	}{
		{
			name: "simpleconfig",
			in:   httpResult{simpleConfig: SimpleConfig{Config: repoowners.Config{Approvers: []string{"Gretel"}}}},
			expectedResult: SimpleConfig{Config: repoowners.Config{
				Approvers:         []string{"hans"},
				Reviewers:         []string{"hans"},
				RequiredReviewers: []string{"hans"},
				Labels:            []string{},
			}},
		},
		{
			name: "fullconfig",
			in:   httpResult{fullConfig: FullConfig{Filters: map[string]repoowners.Config{"tld": {}}}},
			expectedResult: FullConfig{Filters: map[string]repoowners.Config{
				"tld": {
					Approvers:         []string{"hans"},
					Reviewers:         []string{"hans"},
					RequiredReviewers: []string{"hans"},
					Labels:            []string{},
				},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assertEqual(t, tc.in.resolveOwnerAliases(cleaner), tc.expectedResult)
		})
	}
}

func noOpCleaner(in []string) []string {
	return in
}
