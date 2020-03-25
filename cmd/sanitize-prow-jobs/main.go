package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"
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
	errChan := make(chan error)

	var errs []error
	errReadingDone := make(chan struct{})
	go func() {
		for err := range errChan {
			errs = append(errs, err)
		}
		close(errReadingDone)
	}()

	wg := sync.WaitGroup{}
	if err := filepath.Walk(prowJobConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			errChan <- fmt.Errorf("Failed to walk file/directory '%s'", path)
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			data, err := ioutil.ReadFile(path)
			if err != nil {
				errChan <- fmt.Errorf("failed to read file %q: %v", path, err)
				return
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal file %q: %v", err, path)
				return
			}

			defaultJobConfig(jobConfig)

			serialized, err := yaml.Marshal(jobConfig)
			if err != nil {
				errChan <- fmt.Errorf("failed to marshal file %q: %v", err, path)
				return
			}

			if err := ioutil.WriteFile(path, serialized, 0644); err != nil {
				errChan <- fmt.Errorf("failed to write file %q: %v", path, err)
				return
			}
		}(path)

		return nil
	}); err != nil {
		return fmt.Errorf("failed to determinize all Prow jobs: %v", err)
	}

	wg.Wait()
	close(errChan)
	<-errReadingDone

	return utilerrors.NewAggregate(errs)
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
			logrus.WithError(err).Fatal("Failed to determinize")
		}
	} else {
		logrus.Fatal("mandatory argument --prow-jobs-dir wasn't set")
	}
}
