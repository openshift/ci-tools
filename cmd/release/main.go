package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/cmd/release"
)

func main() {
	cmd, err := release.NewCommand()
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
