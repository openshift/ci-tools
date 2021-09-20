// The purpose of this tool is to read a peribolos configuration
// file, get the admins/members of a given organization and
// update the users of a specific group in an Openshift cluster.
package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator"
)

func main() {
	cmd := jobrunaggregator.NewJobAggregatorCommand()
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
