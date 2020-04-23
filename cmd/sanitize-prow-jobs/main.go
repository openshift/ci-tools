package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"
)

type options struct {
	prowJobConfigDir string
	configPath       string

	help bool
}

// Config is the configuration file of this tools, which defines the cluster parameter for each Prow job, i.e., where it runs
type Config struct {
	// the cluster context name if no other condition matches
	Default string `json:"default"`
	// the cluster context name for non Kubernetes jobs
	NonKubernetes string `json:"nonKubernetes"`
	// Groups maps a group of jobs to a cluster
	Groups map[string]Group `json:"groups"`
}

//Group is a group of jobs
type Group struct {
	// a list of job names
	Jobs []string `json:"jobs"`
	// a list of regexes of the file paths
	Paths []string `json:"paths"`

	PathREs []*regexp.Regexp `json:"-"`
}

func (config *Config) getClusterForJob(jobBase prowconfig.JobBase, path string) string {
	if jobBase.Agent != "kubernetes" {
		return config.NonKubernetes
	}
	for cluster, group := range config.Groups {
		for _, job := range group.Jobs {
			if jobBase.Name == job {
				return cluster
			}
		}
	}
	for cluster, group := range config.Groups {
		for _, re := range group.PathREs {
			if re.MatchString(path) {
				return cluster
			}
		}
	}
	return config.Default
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flag.StringVar(&opt.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	return opt
}

func loadConfig(configPath string) (*Config, error) {
	config := &Config{}
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read the config file %q", configPath)
	}
	err = yaml.Unmarshal(data, config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal the config %q", string(data))
	}

	var errs []error
	for cluster, group := range config.Groups {
		var pathREs []*regexp.Regexp
		for i, p := range group.Paths {
			re, err := regexp.Compile(p)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "failed to compile regex config.Groups[%s].Paths[%d] from %q", cluster, i, p))
				continue
			}
			pathREs = append(pathREs, re)
		}
		group.PathREs = pathREs
		config.Groups[cluster] = group
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	return config, nil
}

func determinizeJobs(prowJobConfigDir string, config *Config) error {
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

			defaultJobConfig(jobConfig, path, config)

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

func defaultJobConfig(jc *prowconfig.JobConfig, path string, config *Config) {
	for k := range jc.PresubmitsStatic {
		for idx := range jc.PresubmitsStatic[k] {
			jc.PresubmitsStatic[k][idx].JobBase.Cluster = config.getClusterForJob(jc.PresubmitsStatic[k][idx].JobBase, path)
		}
	}
	for k := range jc.PostsubmitsStatic {
		for idx := range jc.PostsubmitsStatic[k] {
			jc.PostsubmitsStatic[k][idx].JobBase.Cluster = config.getClusterForJob(jc.PostsubmitsStatic[k][idx].JobBase, path)
		}
	}
	for idx := range jc.Periodics {
		jc.Periodics[idx].JobBase.Cluster = config.getClusterForJob(jc.Periodics[idx].JobBase, path)
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

	if len(opt.prowJobConfigDir) == 0 {
		logrus.Fatal("mandatory argument --prow-jobs-dir wasn't set")
	}
	if len(opt.configPath) == 0 {
		logrus.Fatal("mandatory argument --config-path wasn't set")
	}

	config, err := loadConfig(opt.configPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load config from %q", opt.configPath)
	}
	if err := determinizeJobs(opt.prowJobConfigDir, config); err != nil {
		logrus.WithError(err).Fatal("Failed to determinize")
	}
}
