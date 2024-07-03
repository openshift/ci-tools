package main

import (
	"flag"
	"fmt"

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

	jobURL := fmt.Sprintf("%s/%s", bucket, *jobPath)
	if err := jobruntimeanalyzer.Run(jobURL); err != nil {
		logrus.WithError(err).Fatal("Failed")
	}
}
