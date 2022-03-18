package main

import (
	"os"
	"testing"
)

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
func TestGatherOptions(t *testing.T) {
	expected := []string{"org/repo", "other-org/other-repo"}
	os.Args = []string{"cmd", "--enable-on-repo=org/repo", "--cache-record-age=100h", "--enable-on-repo=other-org/other-repo"}

	actual := gatherOptions()

	if !stringSlicesEqual(actual.enableOnRepo, expected) {
		t.Errorf("Test failed, expected: '%+v', got:  '%+v'", expected, actual.enableOnRepo)
	}
}
