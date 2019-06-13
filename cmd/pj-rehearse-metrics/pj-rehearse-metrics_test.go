package main

import (
	"k8s.io/apimachinery/pkg/util/diff"
	"testing"
)

var testGcsPrInfo = &runInfo{gcsPrDir: "pr-logs/pull/openshift_release/3200/"}
var testGcsRunInfo = &runInfo{
	gcsPrDir:  "pr-logs/pull/openshift_release/3200/",
	gcsRunDir: "pr-logs/pull/openshift_release/3200/pull-ci-openshift-release-master-pj-rehearse/123",
}

func TestGcsJobDir(t *testing.T) {
	expected := "pr-logs/pull/openshift_release/3200/pull-ci-openshift-release-master-pj-rehearse/"
	actual := testGcsPrInfo.gcsJobDir()
	if actual != expected {
		t.Errorf("GCS Job Dir path differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestPrNumber(t *testing.T) {
	expected := "3200"
	actual := testGcsPrInfo.prNumber()
	if actual != expected {
		t.Errorf("PR number differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestLocalPrDir(t *testing.T) {
	expected := "/path/to/base/3200"
	actual := testGcsPrInfo.localPrDir("/path/to/base")
	if actual != expected {
		t.Errorf("Local PR directory path differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestGcsMetricsArtifact(t *testing.T) {
	expected := "pr-logs/pull/openshift_release/3200/pull-ci-openshift-release-master-pj-rehearse/123/artifacts/rehearse-metrics.json"
	actual := testGcsRunInfo.gcsMetricsArtifact()
	if actual != expected {
		t.Errorf("GCS metrics artifact path differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestRunNumber(t *testing.T) {
	expected := "123"
	actual := testGcsRunInfo.runNumber()
	if actual != expected {
		t.Errorf("Run number differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestLocalMetricsArtifact(t *testing.T) {
	expected := "/path/to/base/3200/123"
	actual := testGcsRunInfo.localMetricsArtifact("/path/to/base")
	if actual != expected {
		t.Errorf("Path to local artifact differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}
