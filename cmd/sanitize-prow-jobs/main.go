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

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type options struct {
	prowJobConfigDir string
	configPath       string

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flag.StringVar(&opt.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	return opt
}

func determinizeJobs(prowJobConfigDir string, config *dispatcher.Config) error {
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
			errChan <- fmt.Errorf("failed to walk file/directory '%s'", path)
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			data, err := gzip.ReadFileMaybeGZIP(path)
			if err != nil {
				errChan <- fmt.Errorf("failed to read file %q: %w", path, err)
				return
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				errChan <- fmt.Errorf("failed to unmarshal file %q: %w", path, err)
				return
			}

			if err := defaultJobConfig(jobConfig, path, config); err != nil {
				errChan <- fmt.Errorf("failed to default job config %q: %w", path, err)
			}

			serialized, err := yaml.Marshal(jobConfig)
			if err != nil {
				errChan <- fmt.Errorf("failed to marshal file %q: %w", path, err)
				return
			}

			if err := ioutil.WriteFile(path, serialized, 0644); err != nil {
				errChan <- fmt.Errorf("failed to write file %q: %w", path, err)
				return
			}
		}(path)

		return nil
	}); err != nil {
		return fmt.Errorf("failed to determinize all Prow jobs: %w", err)
	}

	wg.Wait()
	close(errChan)
	<-errReadingDone

	return utilerrors.NewAggregate(errs)
}

func defaultJobConfig(jc *prowconfig.JobConfig, path string, config *dispatcher.Config) error {
	for k := range jc.PresubmitsStatic {
		for idx := range jc.PresubmitsStatic[k] {
			cluster, err := config.GetClusterForJob(jc.PresubmitsStatic[k][idx].JobBase, path)
			if err != nil {
				return err
			}
			jc.PresubmitsStatic[k][idx].JobBase.Cluster = string(cluster)
		}
	}
	for k := range jc.PostsubmitsStatic {
		for idx := range jc.PostsubmitsStatic[k] {
			cluster, err := config.GetClusterForJob(jc.PostsubmitsStatic[k][idx].JobBase, path)
			if err != nil {
				return err
			}
			jc.PostsubmitsStatic[k][idx].JobBase.Cluster = string(cluster)
		}
	}
	for idx := range jc.Periodics {
		cluster, err := config.GetClusterForJob(jc.Periodics[idx].JobBase, path)
		if err != nil {
			return err
		}
		jc.Periodics[idx].JobBase.Cluster = string(cluster)
	}
	return nil
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("Failed to parse flags")
	}

	if opt.help {
		flagSet.Usage()
		os.Exit(0)
	}

	if len(opt.prowJobConfigDir) == 0 {
		logrus.Fatal("mandatory argument --prow-jobs-dir wasn't set")
	}
	if len(opt.configPath) == 0 {
		logrus.Fatal("mandatory argument --config-path wasn't set")
	}

	config, err := dispatcher.LoadConfig(opt.configPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load config from %q", opt.configPath)
	}
	if err := config.Validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate the config")
	}
	if err := determinizeJobs(opt.prowJobConfigDir, config); err != nil {
		logrus.WithError(err).Fatal("Failed to determinize")
	}
}
