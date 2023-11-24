package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestGetLDAPUsers(t *testing.T) {
	expected := []string{"alvaro-ldap", "steve-ldap"}
	mapping, errs := createLDAPMapping("testdata/ldapsearch.out")
	if len(errs) > 0 {
		t.Fatalf("got unexpected errors: %v", errs)
	}
	actual, errs := getAllSecretUsers("testdata/test-repo", ".", mapping)
	if len(errs) > 0 {
		t.Fatalf("got unexpected errors: %v", errs)
	}
	if diff := cmp.Diff(expected, sets.List(actual)); diff != "" {
		t.Errorf("expected doesn't match actual, diff: %s", diff)
	}
}

func TestSaveMapping(t *testing.T) {
	expected := `a: b
c: d
`
	dir := t.TempDir()
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("failed to remove the temp dir: %v", err)
		}
	}()
	file := filepath.Join(dir, "mapping.yaml")
	err := saveMapping(file, map[string]string{"a": "b", "c": "d"})
	if err != nil {
		t.Fatalf("got unexpected errors: %v", err)
	}
	bytes, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("got unexpected errors: %v", err)
	}
	actual := string(bytes)
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("expected doesn't match actual, diff: %s", diff)
	}
}
