package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/registry"
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

// generateJobsToDir returns a callback that knows how to generate prow job configuration
// into the dir provided by consuming ci-operator configuration.
//
// Returned callback will cache Prowgen config reads, including unsuccessful attempts
// The keys are either `org` or `org/repo`, and if present in the cache, a previous
// execution of the callback already made an attempt to read a prowgen config in the
// appropriate location, and either stored a pointer to the parsed config if if was
// successfully read, or stored `nil` when the prowgen config could not be read (usually
// because the drop-in is not there).
func generateJobsToDir(dir string, resolver registry.Resolver) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	// Return a closure so the cache is shared among callback calls
	cache := map[string]*config.Prowgen{}
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
		return jc.WriteToDir(dir, info.Org, info.Repo, prowgen.GenerateJobs(configSpec, pInfo))
	}
}

func getReleaseRepoDir(directory string) (string, error) {
	tentative := filepath.Join(build.Default.GOPATH, "src/github.com/openshift/release", directory)
	if stat, err := os.Stat(tentative); err == nil && stat.IsDir() {
		return tentative, nil
	}
	return "", fmt.Errorf("%s is not an existing directory", tentative)
}

func pruneStaleJobs(jobDir, subDir string) error {
	if err := jc.OperateOnJobConfigSubdir(jobDir, subDir, func(jobConfig *prowconfig.JobConfig, info *jc.Info) error {
		pruned := prowgen.Prune(jobConfig)

		if len(pruned.PresubmitsStatic) == 0 && len(pruned.PostsubmitsStatic) == 0 && len(pruned.Periodics) == 0 {
			if err := os.Remove(info.Filename); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			if err := jc.WriteToFile(info.Filename, pruned); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return err
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

	if err := opt.process(); err != nil {
		logrus.WithError(err).Fatal("Failed to process arguments")
	}

	args := flagSet.Args()
	if len(args) == 0 {
		args = append(args, "")
	}
	genJobs := generateJobsToDir(opt.toDir, opt.resolver)
	for _, subDir := range args {
		if err := opt.OperateOnCIOperatorConfigDir(filepath.Join(opt.fromDir, subDir), genJobs); err != nil {
			fields := logrus.Fields{"target": opt.toDir, "source": opt.fromDir}
			logrus.WithError(err).WithFields(fields).Fatal("Failed to generate jobs")
		}

		if opt.ProcessAll() {
			if err := pruneStaleJobs(opt.toDir, subDir); err != nil {
				fields := logrus.Fields{"target": opt.toDir, "source": opt.fromDir}
				logrus.WithError(err).WithFields(fields).Fatal("Failed to prune stale generated jobs")
			}
		}
	}
}
