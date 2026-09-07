package main

import (
	"flag"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/yaml"

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
	fromFiles       flagutil.Strings

	toDir         string
	toReleaseRepo bool

	registryPath string
	resolver     registry.Resolver

	knownInfraJobFiles flagutil.Strings

	managedReposConfigFile string
	managedRepos           *prowgen.ManagedReposConfig

	help bool
}

func bindOptions(flag *flag.FlagSet) *options {
	opt := &options{}

	flag.StringVar(&opt.fromDir, "from-dir", "", "Path to a directory with a directory structure holding ci-operator configuration files for multiple components")
	flag.BoolVar(&opt.fromReleaseRepo, "from-release-repo", false, "If set, it behaves like --from-dir=$GOPATH/src/github.com/openshift/release/ci-operator/config")
	flag.Var(&opt.fromFiles, "from-file", "Path to a ci-operator config file. Can be passed multiple times. Mutually exclusive with --from-{dir,release-repo}. Each file writes atomically to --to-dir.")

	flag.StringVar(&opt.toDir, "to-dir", "", "Path to a directory with a directory structure holding Prow job configuration files for multiple components")
	flag.BoolVar(&opt.toReleaseRepo, "to-release-repo", false, "If set, it behaves like --to-dir=$GOPATH/src/github.com/openshift/release/ci-operator/jobs")

	flag.StringVar(&opt.registryPath, "registry", "", "Path to the step registry directory")

	flag.BoolVar(&opt.help, "h", false, "Show help for ci-operator-prowgen")

	flag.Var(&opt.knownInfraJobFiles, "known-infra-file", "Name of a known infra-file that will not be acted on. Can be passed multiple times.")

	flag.StringVar(&opt.managedReposConfigFile, "managed-repos-config", "", "Path to a YAML file (see pkg/prowgen.ManagedReposConfig) listing org/repo (and, optionally, branches within them) that are managed elsewhere (e.g. onboarded onto EFS). Those entries are skipped entirely: no jobs generated, and existing job files for them in --to-dir are left untouched (not pruned).")

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

	fromFiles := o.fromFiles.Strings()

	if len(fromFiles) == 0 && o.fromDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--from-{dir,release-repo,file}` options")
	}

	if len(fromFiles) > 0 && o.fromDir != "" {
		return fmt.Errorf("ci-operator-prowgen accepts only one of `--from-{dir,release-repo}` and `--from-file` options")
	}

	if o.toDir == "" {
		return fmt.Errorf("ci-operator-prowgen needs exactly one of `--to-{dir,release-repo}` options")
	}

	if len(fromFiles) == 0 {
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
	if len(fromFiles) == 0 {
		if o.managedRepos, err = prowgen.LoadManagedReposConfig(o.managedReposConfigFile); err != nil {
			return fmt.Errorf("--managed-repos-config error: %w", err)
		}
	}
	return nil
}

func (o *options) generateJobsFromFiles() error {
	var errs []error
	for _, path := range o.fromFiles.Strings() {
		if err := o.generateJobsFromFile(path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (o *options) generateJobsFromFile(path string) error {
	logrus.Infof("Reading config from %s", path)
	data, err := gzip.ReadFileMaybeGZIP(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	var configSpec cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &configSpec); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return o.generateJobsFromConfig(path, configSpec)
}

func (o *options) generateJobsFromConfig(source string, configSpec cioperatorapi.ReleaseBuildConfiguration) error {
	info := configSpec.Metadata
	if info.Org == "" || info.Repo == "" || info.Branch == "" {
		return fmt.Errorf("zz_generated_metadata in %s must specify org, repo, and branch", source)
	}
	for name, v := range map[string]string{"org": info.Org, "repo": info.Repo} {
		if v != filepath.Base(v) || v == "." || v == ".." {
			return fmt.Errorf("zz_generated_metadata in %s has invalid %s %q", source, name, v)
		}
	}
	orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	logrus.Infof("Generating jobs for %s@%s", orgRepo, info.Branch)

	generated, err := resolveAndGenerate(o.resolver, &configSpec, &info)
	if err != nil {
		return fmt.Errorf("failed to generate jobs for %s@%s: %w", orgRepo, info.Branch, err)
	}
	if err := jc.WriteToDir(o.toDir, info.Org, info.Repo, generated, prowgen.Generator, nil, jc.WriteToFileAtomic, false); err != nil {
		return fmt.Errorf("failed to write jobs for %s@%s: %w", orgRepo, info.Branch, err)
	}
	return nil
}

func resolveAndGenerate(resolver registry.Resolver, configSpec *cioperatorapi.ReleaseBuildConfiguration, info *cioperatorapi.Metadata) (*prowconfig.JobConfig, error) {
	var clusterProfileResolver func(name string) (*cioperatorapi.ClusterProfile, error) = func(name string) (*cioperatorapi.ClusterProfile, error) {
		return nil, fmt.Errorf("cluster profile resolver not available")
	}
	if resolver != nil {
		resolved, err := registry.ResolveConfig(resolver, *configSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve configuration: %w", err)
		}
		configSpec = &resolved
		clusterProfileResolver = func(name string) (*cioperatorapi.ClusterProfile, error) {
			cp, err := resolver.ResolveClusterProfile(name)
			if err != nil {
				return nil, err
			}
			return &cp, nil
		}
	}
	return prowgen.GenerateJobs(configSpec, info, clusterProfileResolver)
}

// generateJobsToDir generates prow job configuration into the dir provided by
// consuming ci-operator configuration.
func (o *options) generateJobsToDir(subDir string) error {
	generated := map[string]*prowconfig.JobConfig{}
	genJobsFunc := generateJobs(o.resolver, o.managedRepos.IsManaged, generated)
	if err := o.OperateOnCIOperatorConfigDir(filepath.Join(o.fromDir, subDir), genJobsFunc); err != nil {
		return fmt.Errorf("failed to generate jobs: %w", err)
	}
	if err := o.OperateOnJobConfigSubdirPaths(o.toDir, subDir, o.knownInfraJobFiles.StringSet(), func(info *jc.Info) error {
		key := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		if o.managedRepos.IsManaged(key, info.Branch) {
			return nil
		}
		if _, ok := generated[key]; !ok {
			generated[key] = &prowconfig.JobConfig{}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to read job directory paths: %w", err)
	}
	return writeToDir(o.toDir, generated)
}

func generateJobs(resolver registry.Resolver, skip func(orgRepo, branch string) bool, output map[string]*prowconfig.JobConfig) func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
	return func(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *config.Info) error {
		orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
		if skip != nil && skip(orgRepo, info.Branch) {
			return nil
		}

		generated, err := resolveAndGenerate(resolver, configSpec, &info.Metadata)
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
			if err := jc.WriteToDir(dir, org, repo, x.v, prowgen.Generator, nil, jc.WriteToFile, true); err != nil {
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

	if fromFiles := opt.fromFiles.Strings(); len(fromFiles) > 0 {
		logger := logrus.WithFields(logrus.Fields{"target": opt.toDir, "source": fromFiles})
		if err := opt.generateJobsFromFiles(); err != nil {
			logger.WithError(err).Fatal("Failed to generate jobs from file(s)")
		}
		return
	}

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
