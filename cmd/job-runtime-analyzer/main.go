package main

import (
	"flag"

	"github.com/sirupsen/logrus"

	jobruntimeanalyzer "github.com/openshift/ci-tools/pkg/job-runtime-analyzer"
)

const defaultJobURL = "https://storage.googleapis.com/test-platform-results/pr-logs/pull/openshift_ci-tools/999/pull-ci-openshift-ci-tools-master-validate-vendor/1283812971092381696"

func main() {
	jobURL := flag.String("job-url", defaultJobURL, "url to a job")
	flag.Parse()

	if err := jobruntimeanalyzer.Run(*jobURL); err != nil {
		logrus.WithError(err).Fatal("Failed")
	}
}
