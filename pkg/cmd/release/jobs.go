package release

import (
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

func newJobCommand(o *options) *cobra.Command {
	var list bool
	ret := cobra.Command{
		Use:   "job",
		Short: "job commands",
		Long:  "Loads Prow job files and writes them to stdout",
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdJob(o, list, args)
		},
	}
	flags := ret.Flags()
	flags.BoolVarP(&list, "list", "l", false, "list file names instead of contents")
	return &ret
}

func cmdJob(o *options, list bool, args []string) error {
	args = o.argsWithPrefixes(config.JobConfigInRepoPath, o.jobConfigPath, args)
	if list {
		return cmdJobList(args)
	} else {
		return cmdJobPrint(args)
	}
}

func cmdJobList(args []string) error {
	for _, p := range args {
		if err := jobconfig.OperateOnJobConfigSubdirPaths("", p, make(sets.Set[string]), func(
			info *jobconfig.Info,
		) error {
			fmt.Println(info.Filename)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to process configuration files: %w", err)
		}
	}
	return nil
}

func cmdJobPrint(args []string) error {
	for _, p := range args {
		if err := jobconfig.OperateOnJobConfigDir(p, make(sets.Set[string]), func(
			job *prowconfig.JobConfig,
			_ *jobconfig.Info,
		) error {
			fmt.Println("---")
			return printYAML(job)
		}); err != nil {
			return fmt.Errorf("failed to process configuration files: %w", err)
		}
	}
	return nil
}
