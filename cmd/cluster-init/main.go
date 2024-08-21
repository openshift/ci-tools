package main

import (
	"github.com/sirupsen/logrus"

	onboardcmd "github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard"
)

func main() {
	if err := onboardcmd.New().Execute(); err != nil {
		logrus.Fatalf("%s", err)
	}
}
