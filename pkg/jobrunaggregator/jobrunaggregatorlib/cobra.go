package jobrunaggregatorlib

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NoArgs(cmd *cobra.Command, args []string) error {
	for _, arg := range args {
		if len(arg) > 0 {
			return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
		}
	}
	return nil
}
