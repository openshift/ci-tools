package main

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGatherOptions(t *testing.T) {
	expected := "config.yaml"
	os.Args = []string{"cmd", "--cache-record-age=100h", "--config-file=config.yaml"}

	actual := gatherOptions()

	if diff := cmp.Diff(expected, actual.configFile); diff != "" {
		t.Errorf("Test failed, expected: '%+v', got:  '%+v'", expected, actual.configFile)
	}
}
