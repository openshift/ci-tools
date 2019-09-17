package main

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
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
			tc.given.resolveOwnerAliases()
			result := tc.given.resolveOwnerAliases()
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
