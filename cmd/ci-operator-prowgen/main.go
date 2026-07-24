package main

import (
	"flag"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type options struct {
	config.Options

	fromDir         string
	fromReleaseRepo bool
	fromFile        string

	toDir         string
	toReleaseRepo bool

	registryPath string
	resolver     registry.Resolver

	knownInfraJobFiles flagutil.Strings

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromDir, "from-dir", "", "Path to a directory with a directory structure holding ci-operator configuration files for multiple components")
	flag.BoolVar(&opt.fromReleaseRepo, "from-release-repo", false, "If set, it behaves like --from-dir=$GOPATH/src/github.com/openshift/release/ci-operator/config")
	flag.StringVar(&opt.fromFile, "from-file", "", "Path to a single ci-operator configuration file (metadata is read from zz_generated_metadata in the file)")

	flag.StringVar(&opt.toDir, "to-dir", "", "Path to a directory with a directory structure holding Prow job configuration files for multiple components")
	flag.BoolVar(&opt.toReleaseRepo, "to-release-repo", false, "If set, it behaves like --to-dir=$GOPATH/src/github.com/openshift/release/ci-operator/jobs")

	flag.StringVar(&opt.registryPath, "registry", "", "Path to the step registry directory")

	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	flag.Var(&opt.knownInfraJobFiles, "known-infra-file", "Name of a known infra-file that will not be acted on. Can be passed multiple times.")

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

	if o.fromFile == "" && o.fromDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--from-{dir,release-repo,file}` options")
	}

	if o.fromFile != "" && o.fromDir != "" {
		return fmt.Errorf("ci-operator-prowgen accepts only one of `--from-{dir,release-repo}` and `--from-file` options")
	}

	if o.toDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{dir,release-repo}` options")
	}

	if o.fromFile == "" {
		// TODO: deprecate --from-dir
		o.ConfigDir = o.fromDir
		if err := o.Options.Validate(); err != nil {
			return fmt.Errorf("failed to validate config options: %w", err)
		}
		if err := o.Options.Complete(); err != nil {
			return fmt.Errorf("failed to complete config options: %w", err)
		}
	}
	if o.registryPath != "" {
		refs, chains, workflows, clusterProfiles, _, _, observers, err := load.Registry(o.registryPath, load.RegistryFlag(0))
		if err != nil {
			return fmt.Errorf("failed to load registry: %w", err)
		}
		o.resolver = registry.NewResolver(refs, chains, workflows, observers, clusterProfiles)
	}
	return nil
}

func (o *options) generateJobsFromFile() error {
	logrus.Infof("Reading config from %s", o.fromFile)
	data, err := gzip.ReadFileMaybeGZIP(o.fromFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	var configSpec cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}
	info := configSpec.Metadata
	if info.Org == "" || info.Repo == "" || info.Branch == "" {
		return fmt.Errorf("zz_generated_metadata in %s must specify org, repo, and branch", o.fromFile)
	}
	logrus.Infof("Loaded config for %s/%s@%s", info.Org, info.Repo, info.Branch)
	if o.resolver != nil {
		resolved, err := registry.ResolveConfig(o.resolver, configSpec)
		if err != nil {
			return fmt.Errorf("failed to resolve configuration: %w", err)
		}
		configSpec = resolved
	}
	configSpec.UnresolvedConfigPath = o.fromFile
	generated, err := prowgen.GenerateJobs(&configSpec, &info)
	if err != nil {
		return err
	}
	orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	logrus.Infof("Generated %d presubmits, %d postsubmits, %d periodics",
		len(generated.PresubmitsStatic[orgRepo]),
		len(generated.PostsubmitsStatic[orgRepo]),
		len(generated.Periodics))
	logrus.Infof("Writing jobs to %s/%s/%s", o.toDir, info.Org, info.Repo)
	if err := jc.WriteBranchToDir(o.toDir, info.Org, info.Repo, generated, prowgen.Generator); err != nil {
		return err
	}
	logrus.Info("Done")
	return nil
}

// generateJobsToDir generates prow job configuration into the dir provided by
// consuming ci-operator configuration.
func (o *options) generateJobsToDir(subDir string) error {
	generated := map[string]*prowconfig.JobConfig{}
	genJobsFunc := generateJobs(o.resolver, generated)
	if err := o.OperateOnCIOperatorConfigDir(filepath.Join(o.fromDir, subDir), genJobsFunc); err != nil {
		return fmt.Errorf("failed to generate jobs: %w", err)
	}
	if err := o.OperateOnJobConfigSubdirPaths(o.toDir, subDir, o.knownInfraJobFiles.StringSet(), func(info *jc.Info) error {
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

func generateJobs(resolver registry.Resolver, output map[string]*prowconfig.JobConfig) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	return func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		var clusterProfileResolver func(name string) (*api.ClusterProfile, error) = func(name string) (*api.ClusterProfile, error) {
			return nil, fmt.Errorf("cluster profile resolver not available")
		}

		if resolver != nil {
			resolved, err := registry.ResolveConfig(resolver, *configSpec)
			if err != nil {
				return fmt.Errorf("failed to resolve configuration: %w", err)
			}
			configSpec = &resolved
			clusterProfileResolver = func(name string) (*api.ClusterProfile, error) {
				cp, err := resolver.ResolveClusterProfile(name)
				if err != nil {
					return nil, err
				}
				return &cp, nil
			}
		}

		generated, err := prowgen.GenerateJobs(configSpec, &info.Metadata, clusterProfileResolver)
		if err != nil {
			return err
		}
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

	if opt.fromFile != "" {
		logger := logrus.WithFields(logrus.Fields{"target": opt.toDir, "source": opt.fromFile})
		if err := opt.generateJobsFromFile(); err != nil {
			logger.WithError(err).Fatal("Failed to generate jobs from file")
		}
	} else {
		args := flagSet.Args()
		if len(args) == 0 {
			args = append(args, "")
		}
		logger := logrus.WithFields(logrus.Fields{"target": opt.toDir, "source": opt.fromDir})
		for _, subDir := range args {
			logger = logger.WithFields(logrus.Fields{"subdir": subDir})
			if err := opt.generateJobsToDir(subDir); err != nil {
				logger.WithError(err).Fatal("Failed to generate jobs")
			}
		}
	}
}
