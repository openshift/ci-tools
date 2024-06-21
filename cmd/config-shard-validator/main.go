package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/mattn/go-zglob"
	"github.com/sirupsen/logrus"

	"k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	_ "sigs.k8s.io/prow/pkg/hook"
	"sigs.k8s.io/prow/pkg/plugins"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	releaseRepoDir string

	config.Options
}

func (o *options) Validate() error {
	if o.releaseRepoDir == "" {
		return errors.New("required flag --release-repo-dir was unset")
	}

	o.ConfigDir = path.Join(o.releaseRepoDir, config.CiopConfigInRepoPath)
	if err := o.Options.Validate(); err != nil {
		return fmt.Errorf("failed to validate config options: %w", err)
	}
	if err := o.Options.Complete(); err != nil {
		return fmt.Errorf("failed to complete config options: %w", err)
	}

	level, err := logrus.ParseLevel(o.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.releaseRepoDir, "release-repo-dir", "", "Path to openshift/release repo.")
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.Options.Bind(fs)
	o.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

type pathWithConfig struct {
	path, configMap string
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	pluginAgent := plugins.ConfigAgent{}
	if err := pluginAgent.Load(path.Join(o.releaseRepoDir, config.PluginConfigInRepoPath), []string{filepath.Dir(config.PluginConfigInRepoPath)}, "_pluginconfig.yaml", true, false); err != nil {
		logrus.WithError(err).Fatal("Error loading Prow plugin config.")
	}
	pcfg := pluginAgent.Config()

	var pathsToCheck []pathWithConfig
	configInfos := map[string]*config.Info{}
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		configInfos[info.Basename()] = info
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load CI Operator configurations.")
	}

	var foundFailures bool
	if err := jobconfig.OperateOnJobConfigDir(path.Join(o.releaseRepoDir, config.JobConfigInRepoPath), make(sets.Set[string]), func(jobConfig *prowconfig.JobConfig, info *jobconfig.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		for _, presubmits := range jobConfig.PresubmitsStatic {
			for _, presubmit := range presubmits {
				if presubmit.Spec != nil {
					if foundFailure := checkSpec(presubmit.Spec, relPath, presubmit.Name, configInfos); foundFailure {
						foundFailures = true
					}
				}
			}
		}
		for _, postsubmits := range jobConfig.PostsubmitsStatic {
			for _, postsubmit := range postsubmits {
				if postsubmit.Spec != nil {
					if foundFailure := checkSpec(postsubmit.Spec, relPath, postsubmit.Name, configInfos); foundFailure {
						foundFailures = true
					}
				}
			}
		}
		for _, periodic := range jobConfig.Periodics {
			if periodic.Spec != nil {
				if foundFailure := checkSpec(periodic.Spec, relPath, periodic.Name, configInfos); foundFailure {
					foundFailures = true
				}
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load Prow job configurations.")
	}

	if errs := validatePaths(pathsToCheck, &pcfg.ConfigUpdater); errs != nil {
		for _, err := range errs {
			logrus.WithError(err).Error("Validation failed")
		}
		foundFailures = true
	}

	if foundFailures {
		logrus.Fatal("Found configurations that do not belong to the correct auto-updating config")
	}
}

func checkSpec(spec *v1.PodSpec, relPath, name string, configInfos map[string]*config.Info) bool {
	var foundFailures bool
	for containerIndex, container := range spec.Containers {
		for _, env := range container.Env {
			if env.Name == "CONFIG_SPEC" && env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil {
				logger := logrus.WithFields(logrus.Fields{
					"source-file": relPath,
					"job":         name,
					"container":   containerIndex,
					"key":         env.ValueFrom.ConfigMapKeyRef.Key,
				})
				configInfo, exists := configInfos[env.ValueFrom.ConfigMapKeyRef.Key]
				if !exists {
					logger.Error("Could not find CI Operator configuration file for that key.")
					foundFailures = true
					continue
				}
				if env.ValueFrom.ConfigMapKeyRef.Name != configInfo.ConfigMapName() {
					logger.WithFields(logrus.Fields{
						"got":      env.ValueFrom.ConfigMapKeyRef.Name,
						"expected": configInfo.ConfigMapName(),
					}).Error("Invalid config map shard for injected CI-Operator config key.")
					foundFailures = true
				}
			}
		}
	}
	return foundFailures
}

func validatePaths(pathsToCheck []pathWithConfig, pcfg *plugins.ConfigUpdater) (errs []error) {
	var globs []interface{ Match(string) bool }
	var globStrings []string
	var configs []plugins.ConfigMapSpec
	path := field.NewPath("config_updater", "maps")
	for s, c := range pcfg.Maps {
		path := path.Child(s)
		if _, ok := c.Clusters[prowv1.DefaultClusterAlias]; ok {
			errs = append(errs, field.Invalid(path.Child("clusters"), prowv1.DefaultClusterAlias, "`default` cluster name is not allowed, a clustername must be explicitly specified"))
		}
		g, err := zglob.New(s)
		if err != nil {
			logrus.WithField("glob", s).WithError(err).Warn("Failed to compile glob matcher.")
			continue
		}
		if g.Match("ci-operator/jobs") && (c.GZIP == nil || !*c.GZIP) {
			errs = append(errs, field.Invalid(path.Child("gzip"), c.GZIP, "field must be set to `true` for jobconfigs"))
		}
		globs = append(globs, g)
		globStrings = append(globStrings, s)
		configs = append(configs, c)
	}
	inputCh := make(chan pathWithConfig)
	errCh := make(chan error)
	produce := func() error {
		defer close(inputCh)
		for _, x := range pathsToCheck {
			inputCh <- x
		}
		return nil
	}
	map_ := func() error {
		for pathToCheck := range inputCh {
			var matches []string
			path := field.NewPath("config_updater", "maps")
			for i, glob := range globs {
				globStr, updateConfig := globStrings[i], configs[i]
				if glob.Match(pathToCheck.path) {
					matches = append(matches, globStr)
					if updateConfig.Name != pathToCheck.configMap {
						errCh <- field.Invalid(path.Child(globStr), "", fmt.Sprintf("File %q matches glob from unexpected ConfigMap %s instead of %s.", pathToCheck.path, updateConfig.Name, pathToCheck.configMap))
					}
				}
			}
			switch len(matches) {
			case 1:
			case 0:
				errCh <- field.Invalid(path, "", fmt.Sprintf("Config file does not belong to any auto-updating config: %s", pathToCheck.path))
			default:
				for _, s := range matches {
					errCh <- field.Invalid(path, s, fmt.Sprintf("File %q matches glob from more than one ConfigMap", pathToCheck.path))
				}
			}
		}
		return nil
	}
	if err := util.ProduceMap(0, produce, map_, errCh); err != nil {
		errs = append(errs, err.(utilerrors.Aggregate).Errors()...)
	}
	return errs
}
