package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins/ownersconfig"
	"k8s.io/test-infra/prow/repoowners"
)

func assertEqual(t *testing.T, actual, expected interface{}) {
	t.Helper()
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("actual differs from expected:\n%s", cmp.Diff(expected, actual, cmp.AllowUnexported(httpResult{})))
	}
}

func TestMakeHeader(t *testing.T) {
	actual := makeHeader("destination", "source", "src-repo")
	expected := `# DO NOT EDIT; this file is auto-generated using https://github.com/openshift/ci-tools.
# Fetched from https://github.com/source/src-repo root OWNERS
# If the repo had OWNERS_ALIASES then the aliases were expanded
# Logins who are not members of 'destination' organization were filtered out
# See the OWNERS docs: https://git.k8s.io/community/contributors/guide/owners.md

`

	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("Actual differs from expected:\n%s", diff)
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
			description: "Simple Config, empty reviewers list gets defaulted to approvers",
			given: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"david", "sig-alIas", "Alice"},
					},
				},
				repoAliases: ra,
			},
			expected: SimpleConfig{
				Config: repoowners.Config{
					Approvers:         []string{"alice", "bob", "carol", "david"},
					Reviewers:         []string{"alice", "bob", "carol", "david"},
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
					Reviewers:         []string{"alice", "bob", "carol", "david"},
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
		{
			description: "Full Config, empty reviewers list gets defaulted to approvers",
			given: httpResult{
				repoAliases: ra,
				fullConfig: FullConfig{
					Filters: map[string]repoowners.Config{
						"abc": {
							Approvers: []string{"david", "sig-alias", "alice"},
						},
					},
				},
			},
			expected: FullConfig{
				Filters: map[string]repoowners.Config{
					"abc": {
						Approvers:         []string{"alice", "bob", "carol", "david"},
						Reviewers:         []string{"alice", "bob", "carol", "david"},
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

/cc @openshift/test-platform
`
	result := getBody([]string{"config/openshift/origin", "jobs/org/repo"}, defaultPRAssignee)

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
	owners              []byte
	customOwners        []byte
	aliases             []byte
	customOwnersAliases []byte
	someError           error
	notFound            error
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

	if filepath == "CUSTOM_OWNERS" {
		return fg.customOwners, nil
	}
	if filepath == "CUSTOM_OWNERS_ALIASES" {
		return fg.customOwnersAliases, nil
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
	fakeCustomOwners := []byte(`---
approvers:
- approver-from-custom-approvers-filename
- approvers-from-custom-approvers-filename-team
`)
	fakeCustomAliases := []byte(`---
aliases:
  approvers-from-custom-approvers-filename-team:
  - teammember-from-custom-approvers-aliases-filename
`)
	someError := fmt.Errorf("some error")
	notFound := &github.FileNotFound{}

	ra := RepoAliases{}
	ra["team-a"] = sets.NewString("aaa1", "aaa2")

	fakeFileGetter := fakeFileGetter{
		owners:              fakeOwners,
		customOwners:        fakeCustomOwners,
		aliases:             fakeOwnersAliases,
		customOwnersAliases: fakeCustomAliases,
		someError:           someError,
		notFound:            notFound,
	}
	testCases := []struct {
		description        string
		given              orgRepo
		filenames          *ownersconfig.Filenames
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
			expectedHTTPResult: httpResult{},
			expectedError:      nil,
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
		{
			description: "from custom filename",
			filenames:   &ownersconfig.Filenames{Owners: "CUSTOM_OWNERS", OwnersAliases: "CUSTOM_OWNERS_ALIASES"},
			expectedHTTPResult: httpResult{
				simpleConfig: SimpleConfig{
					Config: repoowners.Config{
						Approvers: []string{"approver-from-custom-approvers-filename", "approvers-from-custom-approvers-filename-team"},
					},
				},
				repoAliases:      repoowners.RepoAliases{"approvers-from-custom-approvers-filename-team": sets.NewString("teammember-from-custom-approvers-aliases-filename")},
				ownersFileExists: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			if tc.filenames == nil {
				tc.filenames = &ownersconfig.FakeFilenames
			}
			actual, err := getOwnersHTTP(fakeFileGetter, tc.given, *tc.filenames)
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

type fakeGithubOrgMemberLister []string

func (fghoml fakeGithubOrgMemberLister) ListOrgMembers(org, role string) ([]github.TeamMember, error) {
	var result []github.TeamMember
	for _, member := range fghoml {
		result = append(result, github.TeamMember{Login: member})
	}
	return result, nil
}

func TestOwnersCleanerFactory(t *testing.T) {
	testCases := []struct {
		name              string
		orgMembers        []string
		unfilteredMembers []string
		expectedResult    []string
	}{
		{
			name:              "Nothing to filter",
			orgMembers:        []string{"a", "b", "c"},
			unfilteredMembers: []string{"a", "b", "c"},
			expectedResult:    []string{"a", "b", "c"},
		},
		{
			name:              "Basic filtering",
			orgMembers:        []string{"a", "b", "d"},
			unfilteredMembers: []string{"a", "b", "c"},
			expectedResult:    []string{"a", "b", ""},
		},
		{
			name:              "Casing is ignored",
			orgMembers:        []string{"aA"},
			unfilteredMembers: []string{"Aa"},
			expectedResult:    []string{"aa"},
		},
	}

	for _, tc := range testCases {
		cleaner, err := ownersCleanerFactory(githubOrg, fakeGithubOrgMemberLister(tc.orgMembers))
		if err != nil {
			t.Fatalf("failed to construct cleaner: %v", err)
		}
		if diff := sets.NewString(cleaner(tc.unfilteredMembers)...).Difference(sets.NewString(tc.expectedResult...)); len(diff) > 0 {
			t.Errorf("Actual result  does not match expected, diff: %v", diff)
		}
	}
}

type loadRepoTestData struct {
	TestDirectory string
	ConfigSubDirs []string
	ExtraDirs     []string
	GitHubOrg     string
	GitHubRepo    string
	Blocklist     blocklist
	ExpectedRepos []orgRepo
}

func TestLoadRepos(t *testing.T) {
	loadRepoTestData := []loadRepoTestData{
		{
			TestDirectory: "testdata/test1",
			ConfigSubDirs: []string{"jobs"},
			GitHubOrg:     "kubevirt",
			GitHubRepo:    "project-infra",
			ExpectedRepos: []orgRepo{
				{
					Directories:  []string{"testdata/test1/jobs/kubevirt/hostpath-provisioner"},
					Organization: "kubevirt",
					Repository:   "hostpath-provisioner",
				},
				{
					Directories:  []string{"testdata/test1/jobs/kubevirt/kubevirt"},
					Organization: "kubevirt",
					Repository:   "kubevirt",
				},
			},
		},
		{
			TestDirectory: "testdata/test2",
			ConfigSubDirs: []string{"jobs", "config", "templates"},
			GitHubOrg:     "openshift",
			GitHubRepo:    "release",
			Blocklist:     blocklist{directories: sets.NewString("testdata/test2/templates/openshift/installer")},
			ExpectedRepos: []orgRepo{
				{
					Directories: []string{
						"testdata/test2/jobs/kubevirt/kubevirt",
						"testdata/test2/config/kubevirt/kubevirt",
					},
					Organization: "kubevirt",
					Repository:   "kubevirt",
				},
				{
					Directories: []string{
						"testdata/test2/jobs/openshift/installer",
						"testdata/test2/config/openshift/installer",
						// "testdata/test2/templates/openshift/installer", // not present due to blocklist
					},
					Organization: "openshift",
					Repository:   "installer",
				},
			},
		},
		{
			TestDirectory: "testdata/test2",
			ConfigSubDirs: []string{"jobs", "config", "templates"},
			GitHubOrg:     "openshift",
			GitHubRepo:    "release",
			Blocklist:     blocklist{orgs: sets.NewString("kubevirt")},
			ExpectedRepos: []orgRepo{
				{
					Directories: []string{
						"testdata/test2/jobs/openshift/installer",
						"testdata/test2/config/openshift/installer",
						"testdata/test2/templates/openshift/installer",
					},
					Organization: "openshift",
					Repository:   "installer",
				},
			},
		},
		{
			TestDirectory: "testdata/test3/ci-operator",
			ConfigSubDirs: []string{"jobs"},
			ExtraDirs:     []string{"testdata/test3/core-services/prow/02_config"},
			GitHubOrg:     "kubevirt",
			GitHubRepo:    "project-infra",
			ExpectedRepos: []orgRepo{
				{
					Directories:  []string{"testdata/test3/ci-operator/jobs/kubevirt/hostpath-provisioner"},
					Organization: "kubevirt",
					Repository:   "hostpath-provisioner",
				},
				{
					Directories:  []string{"testdata/test3/ci-operator/jobs/kubevirt/kubevirt", "testdata/test3/core-services/prow/02_config/kubevirt/kubevirt"},
					Organization: "kubevirt",
					Repository:   "kubevirt",
				},
			},
		},
		{
			TestDirectory: "testdata/test4/ci-operator",
			ConfigSubDirs: []string{"jobs"},
			ExtraDirs:     []string{"testdata/test4/core-services/prow/02_config"},
			GitHubOrg:     "kubevirt",
			GitHubRepo:    "project-infra",
			ExpectedRepos: []orgRepo{
				{
					Directories:  []string{"testdata/test4/core-services/prow/02_config/kubevirt/kubevirt"},
					Organization: "kubevirt",
					Repository:   "kubevirt",
				},
			},
		},
	}
	for _, data := range loadRepoTestData {
		repos, err := loadRepos(data.TestDirectory, data.Blocklist, data.ConfigSubDirs, data.ExtraDirs, data.GitHubOrg, data.GitHubRepo)
		if err != nil {
			t.Fatalf("%s: failed to load repos: %v", data.TestDirectory, err)
		}
		if diff := cmp.Diff(repos, data.ExpectedRepos, cmp.AllowUnexported(httpResult{}),
			cmpopts.SortSlices(func(a, b orgRepo) bool {
				return fmt.Sprintf("%+v", a) < fmt.Sprintf("%+v", b)
			}),
			cmpopts.SortSlices(func(a, b string) bool { return a < b }),
		); diff != "" {
			t.Errorf("expected differs from actual: %s", diff)
		}
	}
}
