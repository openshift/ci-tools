package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/prowgen"
)

// selectionCase describes configuration selection and its expected result.
type selectionCase struct {
	name         string
	repo         string
	metadataRepo string
	branches     []string
	want         []string
	wantError    string
}

// TestSelectConfigs checks selection of branches and all their variants.
func TestSelectConfigs(t *testing.T) {
	tests := []selectionCase{
		{
			name:         "selects every main variant and skips other branches",
			repo:         "org/repo",
			metadataRepo: "repo",
			branches:     []string{"main"},
			want:         []string{"org/repo/main.yaml", "org/repo/main__variant.yaml"},
		},
		{
			name:         "mismatched metadata",
			repo:         "org/repo",
			metadataRepo: "other",
			branches:     []string{"main"},
			wantError:    "metadata does not match",
		},
		{
			name:      "repository traversal",
			repo:      "../repo",
			wantError: "invalid repository",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// run checks selection with normal, variant and release configurations.
func (tc selectionCase) run(t *testing.T) {
	source, destination := t.TempDir(), filepath.Join(t.TempDir(), "selected")
	configs := []struct{ name, branch, variant string }{
		{name: "main.yaml", branch: "main"},
		{name: "main__variant.yaml", branch: "main", variant: "variant"},
		{name: "release.yaml", branch: "release-4.22"},
	}
	for _, config := range configs {
		data := "zz_generated_metadata:\n  org: org\n  repo: " + tc.metadataRepo + "\n  branch: " + config.branch + "\n  variant: " + config.variant + "\n"
		writeTestFile(t, filepath.Join(source, "ci-operator/config/org/repo", config.name), data)
	}
	cfg := &prowgen.ManagedReposConfig{Repos: map[string]prowgen.ManagedRepo{tc.repo: {Branches: tc.branches}}}
	count, err := selectConfigs(source, destination, cfg)
	checkError(t, err, tc.wantError)
	if tc.wantError != "" {
		return
	}
	if count != len(tc.want) {
		t.Fatalf("selected %d configurations, want %d", count, len(tc.want))
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatal(err)
	}
	got, err := jobFiles(destination)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(tc.want, sets.List(got)); diff != "" {
		t.Errorf("unexpected selected paths (-want +got):\n%s", diff)
	}
}

// inventoryCase describes a stored inventory and whether it is valid.
type inventoryCase struct {
	name      string
	data      string
	want      []string
	wantError string
	missing   bool
}

// TestReadInventory checks canonical path validation before files can be owned or deleted.
func TestReadInventory(t *testing.T) {
	tests := []inventoryCase{
		{
			name:    "missing inventory",
			missing: true,
			want:    []string{},
		},
		{
			name: "sorted unique paths",
			data: "org/repo/z.yaml\norg/repo/a.yaml\norg/repo/z.yaml\n",
			want: []string{"org/repo/a.yaml", "org/repo/z.yaml"},
		},
		{
			name:      "traversal",
			data:      "org/../escape.yaml\n",
			wantError: "invalid inventory path",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// run reads an isolated inventory fixture.
func (tc inventoryCase) run(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory")
	if !tc.missing {
		writeTestFile(t, path, tc.data)
	}
	got, err := readInventory(path)
	checkError(t, err, tc.wantError)
	if tc.wantError == "" {
		if diff := cmp.Diff(tc.want, sets.List(got)); diff != "" {
			t.Errorf("unexpected inventory (-want +got):\n%s", diff)
		}
	}
}

// candidateCase describes a preview artifact destination.
type candidateCase struct {
	name      string
	location  string
	symlink   bool
	wantError string
}

// TestSaveCandidate checks that previews cannot write into watched EFS paths.
func TestSaveCandidate(t *testing.T) {
	tests := []candidateCase{
		{
			name:     "outside EFS",
			location: "preview",
		},
		{
			name:      "symlink into EFS",
			location:  "linked/preview",
			symlink:   true,
			wantError: "outside the EFS root",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// run saves a candidate into the configured destination.
func (tc candidateCase) run(t *testing.T) {
	base := t.TempDir()
	root, candidate := filepath.Join(base, "efs"), filepath.Join(base, "candidate")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(candidate, "org/repo/jobs.yaml"), "jobs")
	if tc.symlink {
		if err := os.Symlink(root, filepath.Join(base, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(base, tc.location)
	err := saveCandidate(candidate, destination, root)
	checkError(t, err, tc.wantError)
	if tc.wantError == "" {
		assertTestFile(t, filepath.Join(destination, "org/repo/jobs.yaml"), "jobs")
	}
}

// writeTestFile creates a fixture and any parent directories.
func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// assertTestFile compares a file with the expected contents.
func assertTestFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, string(data)); diff != "" {
		t.Errorf("unexpected %s (-want +got):\n%s", path, diff)
	}
}

// checkError checks the expected error substring or the absence of an error.
func checkError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
