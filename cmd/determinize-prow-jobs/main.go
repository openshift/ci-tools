package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	prowconfig "k8s.io/test-infra/prow/config"

	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

const defaultCluster = "api.ci"

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

		if info.IsDir() && filepath.Clean(filepath.Dir(filepath.Dir(path))) == filepath.Clean(prowJobConfigDir) {
			var jobConfig *prowconfig.JobConfig
			if jobConfig, err = jc.ReadFromDir(path); err != nil {
				return fmt.Errorf("failed to read Prow job config from '%s' (%v)", path, err)
			}

			defaultJobConfig(jobConfig)

			repo := filepath.Base(path)
			org := filepath.Base(filepath.Dir(path))
			if err := jc.WriteToDir(prowJobConfigDir, org, repo, jobConfig); err != nil {
				return fmt.Errorf("failed to write Prow job config to '%s' (%v)", path, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to determinize all Prow jobs: %v", err)
	}

	return nil
}

func defaultJobConfig(jc *prowconfig.JobConfig) {
	for k := range jc.PresubmitsStatic {
		for idx := range jc.PresubmitsStatic[k] {
			defaultJobBase(&jc.PresubmitsStatic[k][idx].JobBase)
		}
	}
	for k := range jc.PostsubmitsStatic {
		for idx := range jc.PostsubmitsStatic[k] {
			defaultJobBase(&jc.PostsubmitsStatic[k][idx].JobBase)
		}
	}
	for idx := range jc.Periodics {
		defaultJobBase(&jc.Periodics[idx].JobBase)
	}
}

func defaultJobBase(jb *prowconfig.JobBase) {

	if jb.Cluster == "" || jb.Cluster == "default" {
		jb.Cluster = defaultCluster
	}

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
		fmt.Fprintln(os.Stderr, "determinize tool needs the --prow-jobs-dir")
		os.Exit(1)
	}
}
