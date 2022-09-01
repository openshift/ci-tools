package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	config.Options

	fromDir         string
	fromReleaseRepo bool

	toDir         string
	toReleaseRepo bool

	registryPath string
	resolver     registry.Resolver

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromDir, "from-dir", "", "Path to a directory with a directory structure holding ci-operator configuration files for multiple components")
	flag.BoolVar(&opt.fromReleaseRepo, "from-release-repo", false, "If set, it behaves like --from-dir=$GOPATH/src/github.com/openshift/release/ci-operator/config")

	flag.StringVar(&opt.toDir, "to-dir", "", "Path to a directory with a directory structure holding Prow job configuration files for multiple components")
	flag.BoolVar(&opt.toReleaseRepo, "to-release-repo", false, "If set, it behaves like --to-dir=$GOPATH/src/github.com/openshift/release/ci-operator/jobs")

	flag.StringVar(&opt.registryPath, "registry", "", "Path to the step registry directory")

	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	opt.Options.Bind(flag)

	return opt
}

func (o *options) process() error {
	var err error

	if o.fromReleaseRepo {
		if o.fromDir, err = getReleaseRepoDir("ci-operator/config"); err != nil {
			return fmt.Errorf("--from-release-repo error: %w", err)
		}
	}

	if o.toReleaseRepo {
		if o.toDir, err = getReleaseRepoDir("ci-operator/jobs"); err != nil {
			return fmt.Errorf("--to-release-repo error: %w", err)
		}
	}

	if o.fromDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--from-{dir,release-repo}` options")
	}

	if o.toDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{dir,release-repo}` options")
	}

	// TODO: deprecate --from-dir
	o.ConfigDir = o.fromDir
	if err := o.Options.Validate(); err != nil {
		return fmt.Errorf("failed to validate config options: %w", err)
	}
	if err := o.Options.Complete(); err != nil {
		return fmt.Errorf("failed to complete config options: %w", err)
	}
	if o.registryPath != "" {
		refs, chains, workflows, _, _, observers, err := load.Registry(o.registryPath, load.RegistryFlag(0))
		if err != nil {
			return fmt.Errorf("failed to load registry: %w", err)
		}
		o.resolver = registry.NewResolver(refs, chains, workflows, observers)
	}
	return nil
}

func readProwgenConfig(path string) (*config.Prowgen, error) {
	var pConfig *config.Prowgen
	b, err := ioutil.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("prowgen config found in path %s but couldn't read the file: %w", path, err)
	}

	if err == nil {
		if err := yaml.Unmarshal(b, &pConfig); err != nil {
			return nil, fmt.Errorf("prowgen config found in path %sbut couldn't unmarshal it: %w", path, err)
		}
	}

	return pConfig, nil
}

// generateJobsToDir generates prow job configuration into the dir provided by
// consuming ci-operator configuration.
func (o *options) generateJobsToDir(subDir string, prowConfig map[string]*config.Prowgen) error {
	generated := map[string]*prowconfig.JobConfig{}
	genJobsFunc := generateJobs(o.resolver, prowConfig, generated)
	if err := o.OperateOnCIOperatorConfigDir(filepath.Join(o.fromDir, subDir), genJobsFunc); err != nil {
		return fmt.Errorf("failed to generate jobs: %w", err)
	}
	if err := o.OperateOnJobConfigSubdirPaths(o.toDir, subDir, func(info *jc.Info) error {
		key := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		if _, ok := generated[key]; !ok {
			generated[key] = &prowconfig.JobConfig{}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to read job directory paths: %w", err)
	}
	return writeToDir(o.toDir, generated)
}

func generateJobs(resolver registry.Resolver, cache map[string]*config.Prowgen, output map[string]*prowconfig.JobConfig) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	return func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		pInfo := &prowgen.ProwgenInfo{Metadata: info.Metadata, Config: config.Prowgen{Private: false, Expose: false}}
		var ok bool
		var err error
		var orgConfig, repoConfig *config.Prowgen

		if orgConfig, ok = cache[info.Org]; !ok {
			if cache[info.Org], err = readProwgenConfig(filepath.Join(info.OrgPath, config.ProwgenFile)); err != nil {
				return err
			}
			orgConfig = cache[info.Org]
		}

		if repoConfig, ok = cache[orgRepo]; !ok {
			if cache[orgRepo], err = readProwgenConfig(filepath.Join(info.RepoPath, config.ProwgenFile)); err != nil {
				return err
			}
			repoConfig = cache[orgRepo]
		}

		switch {
		case orgConfig != nil:
			pInfo.Config = *orgConfig
		case repoConfig != nil:
			pInfo.Config = *repoConfig
		}
		if resolver != nil {
			resolved, err := registry.ResolveConfig(resolver, *configSpec)
			if err != nil {
				return fmt.Errorf("failed to resolve configuration: %w", err)
			}
			configSpec = &resolved
		}
		generated := prowgen.GenerateJobs(configSpec, pInfo)
		if o, ok := output[orgRepo]; ok {
			jc.Append(o, generated)
		} else {
			output[orgRepo] = generated
		}
		return nil
	}
}

func getReleaseRepoDir(directory string) (string, error) {
	tentative := filepath.Join(build.Default.GOPATH, "src/github.com/openshift/release", directory)
	if stat, err := os.Stat(tentative); err == nil && stat.IsDir() {
		return tentative, nil
	}
	return "", fmt.Errorf("%s is not an existing directory", tentative)
}

func writeToDir(dir string, c map[string]*prowconfig.JobConfig) error {
	type item struct {
		k string
		v *prowconfig.JobConfig
	}
	ch := make(chan item)
	produce := func() error {
		defer close(ch)
		for k, v := range c {
			ch <- item{k, v}
		}
		return nil
	}
	errCh := make(chan error)
	map_ := func() error {
		for x := range ch {
			i := strings.Index(x.k, "/")
			org, repo := x.k[:i], x.k[i+1:]
			if err := jc.WriteToDir(dir, org, repo, x.v, prowgen.Generator, nil); err != nil {
				errCh <- err
			}
		}
		return nil
	}
	return util.ProduceMap(0, produce, map_, errCh)
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

	if err := opt.process(); err != nil {
		logrus.WithError(err).Fatal("Failed to process arguments")
	}

	args := flagSet.Args()
	if len(args) == 0 {
		args = append(args, "")
	}
	logger := logrus.WithFields(logrus.Fields{"target": opt.toDir, "source": opt.fromDir})
	config := map[string]*config.Prowgen{}
	for _, subDir := range args {
		logger = logger.WithFields(logrus.Fields{"subdir": subDir})
		if err := opt.generateJobsToDir(subDir, config); err != nil {
			logger.WithError(err).Fatal("Failed to generate jobs")
		}
	}
}
