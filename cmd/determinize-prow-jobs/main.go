package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	prowconfig "k8s.io/test-infra/prow/config"

	jc "github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
)

type options struct {
	prowJobConfigDir string

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	return opt
}

func determinizeJobs(prowJobConfigDir string) error {
	if err := filepath.Walk(prowJobConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to walk file/directory '%s'", path)
			return nil
		}

		if !info.IsDir() && filepath.Ext(path) == ".yaml" {
			var jobConfig *prowconfig.JobConfig
			if jobConfig, err = jc.ReadFromFile(path); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read Prow job config from '%s' (%v)", path, err)
				return nil
			}
			if err := jc.WriteToFile(path, jobConfig); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to write Prow job config to '%s' (%v)", path, err)
				return nil
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("Failed to determinize all Prow jobs")
	}

	return nil
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	flagSet.Parse(os.Args[1:])

	if opt.help {
		flagSet.Usage()
		os.Exit(0)
	}

	if len(opt.prowJobConfigDir) > 0 {
		if err := determinizeJobs(opt.prowJobConfigDir); err != nil {
			fmt.Fprintf(os.Stderr, "determinize failed (%v)\n", err)

		}
	} else {
		fmt.Fprintf(os.Stderr, "determinize tool needs the --prow-jobs-dir\n")
		os.Exit(1)
	}
}
