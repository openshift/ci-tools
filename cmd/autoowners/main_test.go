package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func assertEqual(t *testing.T, actual, expected interface{}) {
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("unexpected result: %+v != %+v", actual, expected)
	}
}

func TestGetRepoRoot(t *testing.T) {
	dir, err := ioutil.TempDir("", "populate-owners-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	root := filepath.Join(dir, "root")
	deep := filepath.Join(root, "a", "b", "c")
	git := filepath.Join(root, ".git")
	err = os.MkdirAll(deep, 0777)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(git, 0777)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("from inside the repository", func(t *testing.T) {
		found, err := getRepoRoot(deep)
		if err != nil {
			t.Fatal(err)
		}
		if found != root {
			t.Fatalf("unexpected root: %q != %q", found, root)
		}
	})

	t.Run("from outside the repository", func(t *testing.T) {
		_, err := getRepoRoot(dir)
		if err == nil {
			t.Fatal(err)
		}
	})
}

func TestOrgRepos(t *testing.T) {
	dir, err := ioutil.TempDir("", "populate-owners-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	repoAB := filepath.Join(dir, "a", "b")
	repoCD := filepath.Join(dir, "c", "d")
	err = os.MkdirAll(repoAB, 0777)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(repoCD, 0777)
	if err != nil {
		t.Fatal(err)
	}

	orgRepos, err := orgRepos(dir)
	if err != nil {
		t.Fatal(err)
	}

	expected := []*orgRepo{
		{
			Directories:  []string{repoAB},
			Organization: "a",
			Repository:   "b",
		},
		{
			Directories:  []string{repoCD},
			Organization: "c",
			Repository:   "d",
		},
	}

	assertEqual(t, orgRepos, expected)
}

func TestGetDirectories(t *testing.T) {
	dir, err := ioutil.TempDir("", "populate-owners-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	repoAB := filepath.Join(dir, "a", "b")
	err = os.MkdirAll(repoAB, 0777)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		input    *orgRepo
		expected *orgRepo
		error    *regexp.Regexp
	}{
		{
			name: "config exists",
			input: &orgRepo{
				Directories:  []string{"some/directory"},
				Organization: "a",
				Repository:   "b",
			},
			expected: &orgRepo{
				Directories:  []string{"some/directory", filepath.Join(dir, "a", "b")},
				Organization: "a",
				Repository:   "b",
			},
		},
		{
			name: "config does not exist",
			input: &orgRepo{
				Directories:  []string{"some/directory"},
				Organization: "c",
				Repository:   "d",
			},
			expected: &orgRepo{
				Directories:  []string{"some/directory"},
				Organization: "c",
				Repository:   "d",
			},
			error: regexp.MustCompile("^stat .*/c/d: no such file or directory"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.input.getDirectories(dir)
			if test.error == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else if !test.error.MatchString(err.Error()) {
				t.Fatalf("unexpected error: %v does not match %v", err, test.error)
			}

			assertEqual(t, test.input, test.expected)
		})
	}
}

func TestInsertSlice(t *testing.T) {
	// test replacing two elements of a slice
	given := []string{"alice", "bob", "carol", "david", "emily"}
	expected := []string{"alice", "bob", "charlie", "debbie", "emily"}
	actual := insertStringSlice([]string{"charlie", "debbie"}, given, 2, 4)
	assertEqual(t, actual, expected)

	// test replacing all elements after the first
	expected = []string{"alice", "eddie"}
	actual = insertStringSlice([]string{"eddie"}, given, 1, len(given))
	assertEqual(t, actual, expected)

	// test invalid begin and end indexes, should return the slice unmodified
	actual = insertStringSlice([]string{}, given, 5, 2)
	assertEqual(t, given, given)
	actual = insertStringSlice([]string{}, given, -1, 2)
	assertEqual(t, given, given)
	actual = insertStringSlice([]string{}, given, 1, len(given)+1)
	assertEqual(t, given, given)
}

func TestResolveAliases(t *testing.T) {
	given := &orgRepo{
		Owners: &owners{Approvers: []string{"alice", "sig-alias", "david"},
			Reviewers: []string{"adam", "sig-alias"}},
		Aliases: &aliases{Aliases: map[string][]string{"sig-alias": {"bob", "carol"}}},
	}
	expected := &orgRepo{
		Owners: &owners{Approvers: []string{"alice", "bob", "carol", "david"},
			Reviewers: []string{"adam", "bob", "carol"}},
		Aliases: &aliases{Aliases: map[string][]string{"sig-alias": {"bob", "carol"}}},
	}
	assertEqual(t, given.resolveOwnerAliases(), expected.Owners)
}

func TestWriteYAML(t *testing.T) {
	dir, err := ioutil.TempDir("", "populate-owners-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	for _, test := range []struct {
		name     string
		filename string
		data     interface{}
		expected string
	}{
		{
			name:     "OWNERS",
			filename: "OWNERS",
			data: &owners{
				Approvers: []string{"alice", "bob"},
			},
			expected: `# prefix 1
# prefix 2

approvers:
- alice
- bob
`,
		},
		{
			name:     "OWNERS overwrite",
			filename: "OWNERS",
			data: &owners{
				Approvers: []string{"bob", "charlie"},
			},
			expected: `# prefix 1
# prefix 2

approvers:
- bob
- charlie
`,
		},
		{
			name:     "OWNERS_ALIASES",
			filename: "OWNERS_ALIASES",
			data: &aliases{
				Aliases: map[string][]string{
					"group-1": {"alice", "bob"},
				},
			},
			expected: `# prefix 1
# prefix 2

aliases:
  group-1:
  - alice
  - bob
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.filename)
			err = writeYAML(
				path,
				test.data,
				[]string{"# prefix 1\n", "# prefix 2\n", "\n"},
			)
			if err != nil {
				t.Fatal(err)
			}

			data, err := ioutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if string(data) != test.expected {
				t.Fatalf("unexpected result:\n---\n%s\n--- != ---\n%s\n---\n", string(data), test.expected)
			}
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
	expect := `The OWNERS file has been synced for the following repo(s):

* config/openshift/origin
* jobs/org/repo

/cc @openshift/openshift-team-developer-productivity-test-platform
`
	result := getBody([]string{"config/openshift/origin", "jobs/org/repo"}, githubTeam)

	if expect != result {
		t.Errorf("body '%s' differs from expected '%s'", result, expect)
	}
}
