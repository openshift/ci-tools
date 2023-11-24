package main

import (
	"flag"
	"os"

	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/cmd/release"
)

func main() {
	cmd := release.NewCommand()
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
