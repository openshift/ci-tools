package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/mattn/go-zglob"
	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/jobconfig"
	"github.com/sirupsen/logrus"
	prowconfig "k8s.io/test-infra/prow/config"
	_ "k8s.io/test-infra/prow/hook"
	"k8s.io/test-infra/prow/plugins"

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
	if err := config.OperateOnCIOperatorConfigDir(path.Join(o.releaseRepoDir, diffs.CIOperatorConfigInRepoPath), func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load CI Operator configurations.")
	}

	if err := jobconfig.OperateOnJobConfigDir(path.Join(o.releaseRepoDir, diffs.JobConfigInRepoPath), func(jobConfig *prowconfig.JobConfig, info *jobconfig.Info) error {
		// we know the path is relative, but there is no API to declare that
		relPath, _ := filepath.Rel(o.releaseRepoDir, info.Filename)
		pathsToCheck = append(pathsToCheck, pathWithConfig{path: relPath, configMap: info.ConfigMapName()})
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not load Prow job configurations.")
	}

	var foundFailures bool
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
