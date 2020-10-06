package main

import (
	"flag"
	"os"

	"github.com/openshift/ci-tools/pkg/prowjob"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"k8s.io/test-infra/prow/interrupts"
)

func main() {
	options := prowjob.ProwJobOptions{}
	var fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	options.AddFlags(fs)
	err := fs.Parse(os.Args[1:])
	if err != nil {
		logrus.WithError(err).Fatal("error parsing flag set")
	}
	fileSystem := afero.NewOsFs()
	err = options.ValidateOptions(fileSystem)
	if err != nil {
		logrus.WithError(err).Fatal("incorrect options")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	if err := options.SubmitJobAndWatchResults(nil, fileSystem); err != nil {
		logrus.WithError(err).Fatal("failed while submitting job or watching its result")
	}
}
