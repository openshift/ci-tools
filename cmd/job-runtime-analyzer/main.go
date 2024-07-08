package main

import (
	"flag"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	jobruntimeanalyzer "github.com/openshift/ci-tools/pkg/job-runtime-analyzer"
)

const (
	defaultJobPath = "pr-logs/pull/openshift_ci-tools/999/pull-ci-openshift-ci-tools-master-validate-vendor/1283812971092381696"
	bucket         = "https://storage.googleapis.com/test-platform-results"
)

func main() {
	jobPath := flag.String("job-path", defaultJobPath, "path to a job in the test-platform-results bucket")
	flag.Parse()

	jobURL := path.Join(bucket, *jobPath)
	if strings.Contains(jobURL, "..") {
		logrus.Fatalf("improper url containing backward reference")
	}
	if err := jobruntimeanalyzer.Run(jobURL); err != nil {
		logrus.WithError(err).Fatal("Failed")
	}
}
