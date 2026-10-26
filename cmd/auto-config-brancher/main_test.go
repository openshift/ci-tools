package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunSteps(t *testing.T) {
	testCases := []struct {
		name     string
		commands [][]string

		expectNeedsPushing  bool
		allowLookupFailures []int
		wantError           string
		wantDeferred        bool
		wantCommits         string
	}{
		{
			name:     "Command changes something",
			commands: [][]string{{"bash", "-c", "echo change >file"}},

			expectNeedsPushing: true,
		},
		{
			name:     "Command doesn't change anything",
			commands: [][]string{{"true", "true"}},

			expectNeedsPushing: false,
		},
		{
			name:               "First command changes, second reverts, no push",
			commands:           [][]string{{"bash", "-c", "echo change >file"}, {"git", "revert", "HEAD"}},
			expectNeedsPushing: false,
		},
		{name: "earlier commit and safe updater changes published", commands: [][]string{{"bash", "-c", "echo earlier >earlier"}, {"bash", "-c", "echo safe >safe; echo lookup-diagnostic >&2; exit 10"}}, allowLookupFailures: []int{1}, expectNeedsPushing: true, wantDeferred: true, wantCommits: "3"},
		{name: "safe updater without changes retains earlier commit", commands: [][]string{{"bash", "-c", "echo earlier >earlier"}, {"bash", "-c", "exit 10"}}, allowLookupFailures: []int{1}, expectNeedsPushing: true, wantDeferred: true, wantCommits: "2"},
		{name: "all lookups fail without diff", commands: [][]string{{"bash", "-c", "exit 10"}}, allowLookupFailures: []int{0}, wantDeferred: true, wantCommits: "1"},
		{name: "net zero diff retains deferred failure", commands: [][]string{{"bash", "-c", "echo change >file; exit 10"}, {"git", "revert", "--no-edit", "HEAD"}}, allowLookupFailures: []int{0}, wantDeferred: true, wantCommits: "3"},
		{name: "later step runs after lookup failure", commands: [][]string{{"bash", "-c", "exit 10"}, {"bash", "-c", "echo later >later"}}, allowLookupFailures: []int{0}, expectNeedsPushing: true, wantDeferred: true, wantCommits: "2"},
		{name: "ordinary failure before updater blocks publication", commands: [][]string{{"bash", "-c", "echo unsafe >unsafe; exit 1"}, {"bash", "-c", "echo safe >safe; exit 2"}}, allowLookupFailures: []int{1}, wantError: "exit status 1", wantCommits: "1"},
		{name: "ordinary later failure blocks earlier eligible commits", commands: [][]string{{"bash", "-c", "echo safe >safe; exit 10"}, {"bash", "-c", "echo unsafe >unsafe; exit 1"}}, allowLookupFailures: []int{0}, wantError: "exit status 1", wantCommits: "2"},
		{name: "unconfigured partial exit code blocks publication", commands: [][]string{{"bash", "-c", "echo unsafe >unsafe; exit 10"}}, wantError: "exit status 10", wantCommits: "1"},
		{name: "unconfigured exit code two blocks publication", commands: [][]string{{"bash", "-c", "echo unsafe >unsafe; exit 2"}}, wantError: "exit status 2", wantCommits: "1"},
		{name: "updater ordinary failure blocks publication", commands: [][]string{{"bash", "-c", "echo unsafe >unsafe; exit 1"}}, allowLookupFailures: []int{0}, wantError: "exit status 1", wantCommits: "1"},
		{name: "invalid updater exit code blocks publication", commands: [][]string{{"bash", "-c", "echo unsafe >unsafe; exit 3"}}, allowLookupFailures: []int{0}, wantError: "exit status 3", wantCommits: "1"},
		{name: "flag or panic exit code blocks publication", commands: [][]string{{"bash", "-c", "exit 2"}}, allowLookupFailures: []int{0}, wantError: "exit status 2", wantCommits: "1"},
		{name: "commit failure blocks publication", commands: [][]string{{"bash", "-c", "git config user.name ''; echo safe >safe; exit 10"}}, allowLookupFailures: []int{0}, wantError: "fail to git commit", wantCommits: "1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			wd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get current workdir: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(wd); err != nil {
					t.Errorf("failed to chdir back to original working dir: %v", err)
				}
			})

			tempDir := t.TempDir()
			if err := os.Chdir(tempDir); err != nil {
				t.Fatalf("failed to chdir into tempdir: %v", err)
			}
			for _, cmd := range [][]string{
				{"init"},
				{"config", "commit.gpgsign", "false"},
				{"config", "user.name", "test"},
				{"config", "user.email", "test@example.com"},
				{"commit", "--allow-empty", "--message", "init"},
			} {
				if out, err := exec.Command("git", cmd...).CombinedOutput(); err != nil {
					t.Fatalf("git command %q failed: %v, out: %s", cmd, err, string(out))
				}
			}
			var steps []step
			for index, command := range tc.commands {
				current := step{command: command[0], arguments: command[1:]}
				for _, allowed := range tc.allowLookupFailures {
					if allowed == index {
						current.allowLookupFailures = true
					}
				}
				steps = append(steps, current)
			}

			var output bytes.Buffer
			result, err := runSteps(steps, "tests <test@test.com>", &output, &output)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error %q, got %v; output: %s", tc.wantError, err, output.String())
				}
				if result.needsPushing || result.lookupFailures != nil {
					t.Fatalf("unsafe result eligible for publication: %+v", result)
				}
			} else if err != nil {
				t.Fatalf("runSteps failed: %v; output: %s", err, output.String())
			}
			if result.needsPushing != tc.expectNeedsPushing {
				t.Errorf("expectNeedsPushing: %t, needsPushing: %t", tc.expectNeedsPushing, result.needsPushing)
			}
			if (result.lookupFailures != nil) != tc.wantDeferred {
				t.Errorf("deferred failure: %v", result.lookupFailures)
			}
			if tc.wantCommits != "" {
				count, err := exec.Command("git", "rev-list", "--count", "HEAD").CombinedOutput()
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(count)) != tc.wantCommits {
					t.Errorf("want %s commits, got %s", tc.wantCommits, count)
				}
			}
			publication := &fakePublication{}
			if err == nil {
				terminal := finishSteps(result, publication)
				if (terminal != nil) != tc.wantDeferred {
					t.Errorf("terminal result lost deferred failure: %v", terminal)
				}
			}
			if publication.called != tc.expectNeedsPushing {
				t.Errorf("publication called: %t", publication.called)
			}
			if tc.expectNeedsPushing {
				status, err := exec.Command("git", "status", "--porcelain").CombinedOutput()
				if err != nil {
					t.Fatal(err)
				}
				if len(status) != 0 {
					t.Errorf("eligible result has uncommitted changes: %s", status)
				}
			}
		})
	}
}

type fakePublication struct {
	called bool
	err    error
}

func (publication *fakePublication) Publish() error {
	publication.called = true
	return publication.err
}

func TestFinishSteps(t *testing.T) {
	lookupError := errors.New("lookup failed")
	publicationError := errors.New("publication failed")
	for _, tc := range []struct {
		name        string
		push        bool
		lookup      error
		publication error
	}{
		{name: "no changes"},
		{name: "no diff still fails", lookup: lookupError},
		{name: "successful publication", push: true},
		{name: "publication precedes deferred failure", push: true, lookup: lookupError},
		{name: "publication failure retains lookup diagnostics", push: true, lookup: lookupError, publication: publicationError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publication := &fakePublication{err: tc.publication}
			err := finishSteps(stepResult{needsPushing: tc.push, lookupFailures: tc.lookup}, publication)
			if publication.called != tc.push {
				t.Errorf("publication called: %t", publication.called)
			}
			if tc.lookup != nil && !errors.Is(err, tc.lookup) {
				t.Errorf("lookup failure lost: %v", err)
			}
			if tc.publication != nil && !errors.Is(err, tc.publication) {
				t.Errorf("publication failure lost: %v", err)
			}
			if tc.lookup == nil && tc.publication == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}
