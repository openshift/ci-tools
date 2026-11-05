package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
)

// generatedJob is a minimal generated shard used to test publisher ownership.
const generatedJob = `presubmits:
  org/repo:
  - name: pull-ci-org-repo-main-unit
    labels:
      ci.openshift.io/generator: prowgen
`

// fakeCommands models only the subprocess boundary; filesystem publication is real.
type fakeCommands struct {
	fail       string
	latest     string
	generation int
	seed       string
	validated  bool
	removed    bool
}

// run generates deterministic candidates and checks the validation snapshot.
func (f *fakeCommands) run(_ context.Context, dir, program string, args ...string) error {
	if program == f.fail {
		return fmt.Errorf("injected %s failure", program)
	}
	switch program {
	case "git":
		if args[0] == "worktree" && args[1] == "add" {
			return filepath.WalkDir(f.latest, treeCopy{source: f.latest, destination: args[3]}.visit)
		}
		if args[0] == "worktree" && args[1] == "remove" {
			f.removed = true
			return os.RemoveAll(args[3])
		}
	case "ci-operator-prowgen":
		candidate := argument(args, "--to-dir")
		file := filepath.Join(candidate, "org/repo/jobs.yaml")
		if f.generation > 0 {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			f.seed = string(data)
		}
		f.generation++
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return err
		}
		return os.WriteFile(file, []byte(generatedJob), 0644)
	case "checkconfig":
		f.validated = true
		paths := argumentValues(args, "--job-config-path")
		if len(paths) != 3 {
			return fmt.Errorf("expected three job-config-path values, got %v", paths)
		}
		if filepath.IsAbs(argument(args, "--supplemental-prow-config-dir")) {
			return errors.New("supplemental directory must be relative to source")
		}
		if _, err := os.Stat(filepath.Join(paths[2], "other/jobs.yaml")); err != nil {
			return fmt.Errorf("unowned jobs missing from validation: %w", err)
		}
	}
	return nil
}

// output returns distinct revisions when an older trigger needs reconciliation.
func (f *fakeCommands) output(_ context.Context, dir, _ string, args ...string) ([]byte, error) {
	if f.latest != "" && (args[1] == "FETCH_HEAD" || strings.Contains(filepath.Base(dir), "prowgen-efs-source.")) {
		return []byte("latest\n"), nil
	}
	return []byte("trigger\n"), nil
}

// treeCopy copies an isolated source fixture for a simulated Git worktree.
type treeCopy struct{ source, destination string }

// visit copies a fixture file without copying directory metadata.
func (c treeCopy) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if entry.IsDir() {
		return nil
	}
	relative, err := filepath.Rel(c.source, path)
	if err != nil {
		return err
	}
	return copyFile(path, filepath.Join(c.destination, relative))
}

// argument returns the first value following a command-line flag.
func argument(args []string, flag string) string {
	values := argumentValues(args, flag)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// argumentValues returns all values following a repeated command-line flag.
func argumentValues(args []string, flag string) []string {
	var values []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			values = append(values, args[i+1])
		}
	}
	return values
}

// runCase describes a publisher run and its existing Git or EFS seed.
type runCase struct {
	name      string
	publish   bool
	empty     bool
	seed      string
	latest    bool
	failure   string
	wantError string
}

// TestRun checks the main workflow, ownership boundaries and validation before writes.
func TestRun(t *testing.T) {
	tests := []runCase{
		{
			name: "preview preserves Git overrides without publishing",
			seed: "git",
		},
		{
			name:    "publish preserves owned overrides and prunes stale jobs",
			publish: true,
			seed:    "owned",
		},
		{
			name:    "unenroll the last managed repository",
			publish: true,
			empty:   true,
			seed:    "owned",
		},
		{
			name:      "refuse to overwrite another writer's file",
			publish:   true,
			seed:      "unowned",
			wantError: "refusing to replace unowned file",
		},
		{
			name:      "refuse a mixed manual and generated Git shard",
			publish:   true,
			seed:      "manual",
			wantError: "separate hand-written jobs",
		},
		{
			name:      "failed validation preserves output and cleans up the latest checkout",
			publish:   true,
			seed:      "owned",
			latest:    true,
			failure:   "checkconfig",
			wantError: "injected checkconfig failure",
		},
		{
			name:    "old trigger publishes the latest managed configuration",
			publish: true,
			latest:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// fixture creates a release checkout, existing jobs and the subprocess boundary.
func (tc runCase) fixture(t *testing.T) (options, *fakeCommands) {
	source, root := t.TempDir(), t.TempDir()
	managed := "repos:\n  org/repo:\n    branches: [main]\n"
	if tc.empty {
		managed = "repos: {}\n"
	}
	writeTestFile(t, filepath.Join(source, "core-services/prow/_managed-repos.yaml"), managed)
	writeTestFile(t, filepath.Join(source, "ci-operator/config/org/repo/main.yaml"), "zz_generated_metadata:\n  org: org\n  repo: repo\n  branch: main\n")
	writeTestFile(t, filepath.Join(root, "other/jobs.yaml"), "periodics: []\n")
	switch tc.seed {
	case "owned", "unowned":
		writeTestFile(t, filepath.Join(root, "generated/org/repo/jobs.yaml"), generatedJob+"# existing overrides\n")
	case "git":
		writeTestFile(t, filepath.Join(source, "ci-operator/jobs/org/repo/jobs.yaml"), generatedJob+"# existing overrides\n")
	case "manual":
		writeTestFile(t, filepath.Join(source, "ci-operator/jobs/org/repo/jobs.yaml"), generatedJob+"periodics:\n- name: manual\n")
	}
	if tc.seed == "owned" {
		writeTestFile(t, filepath.Join(root, "..prowgen/inventory"), "org/repo/jobs.yaml\norg/repo/stale.yaml\n")
		writeTestFile(t, filepath.Join(root, "generated/org/repo/stale.yaml"), generatedJob)
	}
	fake := &fakeCommands{fail: tc.failure}
	if tc.latest {
		fake.latest = t.TempDir()
		if err := filepath.WalkDir(source, treeCopy{source: source, destination: fake.latest}.visit); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(source, "core-services/prow/_managed-repos.yaml"), "repos: {}\n")
	}
	return options{
		releaseRepo:   source,
		managedConfig: "core-services/prow/_managed-repos.yaml",
		efsRoot:       root,
		candidateDir:  filepath.Join(t.TempDir(), "preview"),
		publish:       tc.publish,
	}, fake
}

// run verifies visible output and cleanup after a publisher run.
func (tc runCase) run(t *testing.T) {
	opts, fake := tc.fixture(t)
	p := publisher{commands: fake, rename: os.Rename}
	err := p.run(context.Background(), opts)
	checkError(t, err, tc.wantError)
	assertTestFile(t, filepath.Join(opts.efsRoot, "other/jobs.yaml"), "periodics: []\n")
	if tc.latest && !fake.removed {
		t.Error("isolated source checkout was not cleaned up")
	}
	output := filepath.Join(opts.efsRoot, "generated/org/repo/jobs.yaml")
	if tc.wantError != "" {
		if tc.seed == "owned" || tc.seed == "unowned" {
			assertTestFile(t, output, generatedJob+"# existing overrides\n")
		}
		return
	}
	if !tc.publish {
		assertTestFile(t, filepath.Join(opts.candidateDir, "org/repo/jobs.yaml"), generatedJob)
		if _, err := os.Stat(filepath.Join(opts.efsRoot, "generated")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("preview changed visible output: %v", err)
		}
	} else if tc.empty {
		if _, err := os.Stat(output); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("expected no generated output: %v", err)
		}
	} else {
		assertTestFile(t, output, generatedJob)
	}
	if tc.publish && !fake.validated {
		t.Error("candidate was not validated")
	}
	if !tc.empty && (tc.seed == "owned" || tc.seed == "git") && fake.seed != generatedJob+"# existing overrides\n" {
		t.Errorf("existing overrides were not seeded: %q", fake.seed)
	}
	if _, err := os.Stat(filepath.Join(opts.efsRoot, "generated/org/repo/stale.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale file remains: %v", err)
	}
}

// interruptedRename fails at a chosen file replacement to exercise recovery.
type interruptedRename struct{ calls, failAt int }

// rename fails once at failAt and otherwise performs a real atomic rename.
func (r *interruptedRename) rename(source, destination string) error {
	r.calls++
	if r.calls == r.failAt {
		return errors.New("interrupted publication")
	}
	return os.Rename(source, destination)
}

// publicationCase describes atomic file replacement and inventory recovery.
type publicationCase struct {
	name   string
	failAt int
}

// TestPublish checks that retries repair interrupted writes and preserve unowned files.
func TestPublish(t *testing.T) {
	tests := []publicationCase{
		{
			name: "successful publication",
		},
		{
			name:   "interrupted after first replacement",
			failAt: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// run publishes real files and retries using the durable recovery inventory.
func (tc publicationCase) run(t *testing.T) {
	root := t.TempDir()
	state, output, candidate := filepath.Join(root, "..prowgen"), filepath.Join(root, "generated"), filepath.Join(root, "..candidate")
	writeTestFile(t, filepath.Join(state, "inventory"), "org/repo/stale.yaml\n")
	writeTestFile(t, filepath.Join(output, "org/repo/stale.yaml"), "stale")
	writeTestFile(t, filepath.Join(output, "other/repo/manual.yaml"), "unowned")
	desired := sets.New("org/repo/a.yaml", "org/repo/b.yaml")
	for _, file := range sets.List(desired) {
		writeTestFile(t, filepath.Join(candidate, file), "new")
	}
	old := sets.New("org/repo/stale.yaml")
	renamer := &interruptedRename{failAt: tc.failAt}
	p := publisher{rename: renamer.rename}
	err := p.publish(state, output, candidate, old, desired)
	wantError := ""
	if tc.failAt > 0 {
		wantError = "interrupted publication"
	}
	checkError(t, err, wantError)
	if tc.failAt > 0 {
		recovery, err := readInventory(filepath.Join(state, "inventory"))
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(sets.List(old.Union(desired)), sets.List(recovery)); diff != "" {
			t.Fatalf("unexpected recovery inventory (-want +got):\n%s", diff)
		}
		for _, file := range sets.List(desired) {
			writeTestFile(t, filepath.Join(candidate, file), "new")
		}
		p.rename = os.Rename
		if err := p.publish(state, output, candidate, recovery, desired); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range sets.List(desired) {
		assertTestFile(t, filepath.Join(output, file), "new")
	}
	assertTestFile(t, filepath.Join(output, "other/repo/manual.yaml"), "unowned")
	if _, err := os.Stat(filepath.Join(output, "org/repo/stale.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale output remains: %v", err)
	}
	inventory, err := readInventory(filepath.Join(state, "inventory"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(sets.List(desired), sets.List(inventory)); diff != "" {
		t.Errorf("unexpected final inventory (-want +got):\n%s", diff)
	}
}
