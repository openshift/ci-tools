package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGetLDAPUsers(t *testing.T) {
	expected := []string{"alvaro-ldap", "steve-ldap"}
	actual, errs := getLdapUsers("testdata/ldapsearch.out", "testdata/test-repo", ".")
	if len(errs) > 0 {
		t.Fatalf("got unexpected errors: %v", errs)
	}
	if diff := cmp.Diff(expected, actual.List()); diff != "" {
		t.Errorf("expected doesn't match actual, diff: %s", diff)
	}
}
