package main

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestReadSchedules(t *testing.T) {
	config, err := readSchedules("testdata/schedules")
	if err != nil {
		t.Fatalf("could not read schedules: %v", err)
	}
	testhelper.CompareWithFixture(t, config)
}
