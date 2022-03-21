package main

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGatherOptions(t *testing.T) {
	expected := []string{"org/repo", "other-org/other-repo"}
	os.Args = []string{"cmd", "--enable-on-repo=org/repo", "--cache-record-age=100h", "--enable-on-repo=other-org/other-repo"}

	actual := gatherOptions()

	if diff := cmp.Diff(expected, actual.enableOnRepos.Strings()); diff != "" {
		t.Errorf("Test failed, expected: '%+v', got:  '%+v'", expected, actual.enableOnRepos)
	}
}
