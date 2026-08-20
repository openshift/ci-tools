package dispatcher

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteGobRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.gob")
	want := map[string]ProwJobData{
		"job-a": {Cluster: "build01", Capabilities: []string{"arm64"}},
		"job-b": {Cluster: "build02"},
	}

	if err := WriteGob(path, want); err != nil {
		t.Fatalf("WriteGob() returned an error: %v", err)
	}
	var got map[string]ProwJobData
	if err := ReadGob(path, &got); err != nil {
		t.Fatalf("ReadGob() returned an error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip differs: got %#v, want %#v", got, want)
	}
}

func TestWriteGobReportsFailureAfterAtomicReplacementAsCommitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.gob")
	want := map[string]ProwJobData{"job-a": {Cluster: "build02"}}
	syncErr := errors.New("directory sync failed")

	err := writeGob(path, want, func(string) error { return syncErr })
	if err == nil {
		t.Fatal("expected directory sync failure")
	}
	if !IsGobWriteCommitted(err) {
		t.Fatalf("directory sync failure was not reported as committed: %v", err)
	}
	if !errors.Is(err, syncErr) {
		t.Fatalf("committed error does not wrap sync failure: %v", err)
	}

	var got map[string]ProwJobData
	if err := ReadGob(path, &got); err != nil {
		t.Fatalf("failed to read atomically replaced Gob: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("atomically replaced Gob differs: got %#v, want %#v", got, want)
	}
}

func TestWriteGobKeepsLastGoodFileWhenEncodingFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.gob")
	want := map[string]ProwJobData{"job-a": {Cluster: "build01"}}
	if err := WriteGob(path, want); err != nil {
		t.Fatalf("failed to write initial Gob: %v", err)
	}

	if err := WriteGob(path, make(chan int)); err == nil {
		t.Fatal("expected unsupported channel value to fail Gob encoding")
	}

	var got map[string]ProwJobData
	if err := ReadGob(path, &got); err != nil {
		t.Fatalf("failed to read last good Gob after failed write: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed write changed last good data: got %#v, want %#v", got, want)
	}
}

func TestGobFilesystemErrorsIncludeOperationContext(t *testing.T) {
	missingDirectory := filepath.Join(t.TempDir(), "missing")
	if err := WriteGob(filepath.Join(missingDirectory, "assignments.gob"), map[string]string{}); err == nil || !strings.Contains(err.Error(), "create temporary Gob file") {
		t.Fatalf("temporary-file creation error lacks context: %v", err)
	}
	if err := syncGobDirectory(missingDirectory); err == nil || !strings.Contains(err.Error(), "open Gob directory for sync") {
		t.Fatalf("directory-open error lacks context: %v", err)
	}
}
