package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	_ "k8s.io/test-infra/prow/hook"
	"k8s.io/test-infra/prow/plugins"

	"github.com/mattn/go-zglob"
	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
	"github.com/openshift/ci-operator/pkg/api"
)

type options struct {
	releaseRepoDir string

	logLevel string
}

func (o *options) Validate() error {
	if o.releaseRepoDir == "" {
		return errors.New("required flag --release-repo-dir was unset")
	}

	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	logrus.SetLevel(level)
	return nil
}

func (o *options) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.releaseRepoDir, "release-repo-dir", "", "Path to openshift/release repo.")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
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
	if err := pluginAgent.Load(path.Join(o.releaseRepoDir, diffs.PluginsInRepoPath)); err != nil {
		logrus.WithError(err).Fatal("Error loading Prow plugin config.")
	}
	pcfg := pluginAgent.Config()

	var pathsToCheck []pathWithConfig
	configInfos := map[string]*config.Info{}
	if err := config.OperateOnCIOperatorConfigDir(path.Join(o.releaseRepoDir, diffs.CIOperatorConfigInRepoPath), func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		configInfos[info.Basename()] = info
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load CI Operator configurations.")
	}

	var foundFailures bool
	if err := jobconfig.OperateOnJobConfigDir(path.Join(o.releaseRepoDir, diffs.JobConfigInRepoPath), func(jobConfig *prowconfig.JobConfig, info *jobconfig.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		for _, presubmits := range jobConfig.Presubmits {
			for _, presubmit := range presubmits {
				if presubmit.Spec != nil {
					if foundFailure := checkSpec(presubmit.Spec, relPath, presubmit.Name, configInfos); foundFailure {
						foundFailures = true
					}
				}
			}
		}
		for _, postsubmits := range jobConfig.Postsubmits {
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

	for _, pathToCheck := range pathsToCheck {
		var matchesAny bool
		logger := logrus.WithField("source-file", pathToCheck.path)
		for glob, updateConfig := range pcfg.ConfigUpdater.Maps {
			globLogger := logger.WithField("glob", glob)
			matches, matchErr := zglob.Match(glob, pathToCheck.path)
			if matchErr != nil {
				globLogger.WithError(matchErr).Warn("Failed to check glob match.")
			}
			if matches {
				if updateConfig.Name != pathToCheck.configMap {
					globLogger.Error("File matches glob from unexpected ConfigMap.")
					foundFailures = true
				}
				matchesAny = true
				break
			}
		}
		if !matchesAny {
			logger.Error("Config file does not belong to any auto-updating config.")
			foundFailures = true
		}
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
