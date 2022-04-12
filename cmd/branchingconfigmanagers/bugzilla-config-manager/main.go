package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"path"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/plugins"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowconfigsharding"
)

type opts struct {
	lifecycleConfigFile string
	pluginConfigDir     string
	overwriteTimeRaw    string
	overwriteTime       *time.Time
}

func gatherOpts() (*opts, error) {
	o := &opts{}
	flag.StringVar(&o.lifecycleConfigFile, "lifecycle-config", "", "Path to the lifecycle config file")
	flag.StringVar(&o.pluginConfigDir, "prow-plugin-config-dir", "", "Path to the Prow plugin configuration directory.")
	flag.StringVar(&o.overwriteTimeRaw, "overwrite-time", "", "Act as if this was the current time, must be in RFC3339 format")
	flag.Parse()

	var errs []error
	if o.lifecycleConfigFile == "" {
		errs = append(errs, errors.New("--lifecycle-config is mandatory"))
	}
	if o.pluginConfigDir == "" {
		errs = append(errs, errors.New("--prow-plugin-config-dir is mandatory"))
	}
	if o.overwriteTimeRaw != "" {
		if parsed, err := time.Parse(time.RFC3339, o.overwriteTimeRaw); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse %q as RFC3339 time: %w", o.overwriteTimeRaw, err))
		} else {
			o.overwriteTime = &parsed
		}
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := gatherOpts()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	configBytes, err := ioutil.ReadFile(opts.lifecycleConfigFile)
	if err != nil {
		logrus.WithError(err).Fatal("failed to read --lifecycle-config")
	}

	var lifecycleConfig ocplifecycle.Config
	if err := yaml.Unmarshal(configBytes, &lifecycleConfig); err != nil {
		logrus.WithError(err).Fatal("failed to deserialize the config")
	}

	configPath := path.Join(opts.pluginConfigDir, config.PluginConfigFile)
	agent := plugins.ConfigAgent{}
	if err := agent.Load(configPath, []string{opts.pluginConfigDir}, "_pluginconfig.yaml", false, true); err != nil {
		logrus.WithError(err).Fatal("failed to load Prow plugin configuration")
	}

	pluginConfig := agent.Config()
	now := time.Now()
	if opts.overwriteTime != nil {
		now = *opts.overwriteTime
	}
	updatedBugzillaConfig, err := run(lifecycleConfig, pluginConfig.Bugzilla, now)
	if err != nil {
		logrus.WithError(err).Fatal("failed to reconcile Bugzilla config")
	}

	pluginConfig.Bugzilla = *updatedBugzillaConfig

	pluginConfig, err = prowconfigsharding.WriteShardedPluginConfig(pluginConfig, afero.NewBasePathFs(afero.NewOsFs(), opts.pluginConfigDir))
	if err != nil {
		logrus.WithError(err).Fatal("failed to write plugin config shards")
	}
	pluginConfigRaw, err := yaml.Marshal(pluginConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to serialize plugin config")
	}

	if err := ioutil.WriteFile(configPath, pluginConfigRaw, 0644); err != nil {
		logrus.WithError(err).Fatal("failed to write main plugin config")
	}
}

var releaseBranchPrefixes = []string{"openshift-", "release-"}
var developmentDependentBugStates = &[]plugins.BugzillaBugState{{Status: "MODIFIED"}, {Status: "ON_QA"}, {Status: "VERIFIED"}}

func run(lifecycleConfig ocplifecycle.Config, currentConfig plugins.Bugzilla, now time.Time) (*plugins.Bugzilla, error) {
	developmentVersion, gaVersions, nonEOLNonGAVersions, err := extractFromConfig(lifecycleConfig, now)
	if err != nil {
		return nil, fmt.Errorf("failed to extract config from lifecycleConfig: %w", err)
	}

	return reconcileConfig(&currentConfig, developmentVersion, gaVersions, nonEOLNonGAVersions), nil
}

func extractFromConfig(lifecycleConfig ocplifecycle.Config, now time.Time) (developmentVersion *ocplifecycle.MajorMinor, gaVersions, nonEOLNonGAVersions []ocplifecycle.MajorMinor, err error) {
	var errs []error
	var developmentVersionFound bool

	allNonEOLVersions := sets.String{}
	gaVersionsSet := sets.String{}
	for ocpProduct, productConfig := range lifecycleConfig {
		if ocpProduct != "ocp" {
			continue
		}

		for productVersion, events := range productConfig {
			allNonEOLVersions.Insert(productVersion)
			for _, event := range events {

				// Future events, ignore
				if event.When == nil || now.Before(event.When.Time) {
					continue
				}

				// End of life version, nothing to do.
				if event.Event == ocplifecycle.LifecycleEventEndOfLife {
					allNonEOLVersions.Delete(productVersion)
					break
				}

				if event.Event == ocplifecycle.LifecycleEventGenerallyAvailable {
					gaVersionsSet.Insert(productVersion)
					break
				}

				if event.Event == ocplifecycle.LifecycleEventCodeFreeze {
					break
				}

				if event.Event == ocplifecycle.LifecycleEventOpen {
					if developmentVersionFound {
						errs = append(errs, fmt.Errorf("found multiple development versions: %s and %s", developmentVersion, productVersion))
					}
					parsedVersion, err := ocplifecycle.ParseMajorMinor(productVersion)
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to parse %s as majorMinor version: %w", productVersion, err))
						continue
					}
					developmentVersion = parsedVersion
					developmentVersionFound = true
				}

			}
		}
	}

	for _, nonEOLVersion := range allNonEOLVersions.List() {
		parsed, err := ocplifecycle.ParseMajorMinor(nonEOLVersion)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse %s as majorMinor: %w", nonEOLVersion, err))
			continue
		}
		if gaVersionsSet.Has(nonEOLVersion) {
			gaVersions = append(gaVersions, *parsed)
		} else {
			nonEOLNonGAVersions = append(nonEOLNonGAVersions, *parsed)
		}
	}

	return developmentVersion, gaVersions, nonEOLNonGAVersions, utilerrors.NewAggregate(errs)
}

func reconcileConfig(cfg *plugins.Bugzilla, developmentVersion *ocplifecycle.MajorMinor, gaVersions, nonEOLNonGAVersions []ocplifecycle.MajorMinor) *plugins.Bugzilla {
	if cfg.Default == nil {
		cfg.Default = map[string]plugins.BugzillaBranchOptions{}
	}

	if developmentVersion != nil {
		// main/master: Set developmentVersion and leave the rest as is
		for _, developmentBranchName := range []string{"main", "master"} {
			config := cfg.Default[developmentBranchName]
			config.TargetRelease = utilpointer.String(developmentVersion.String() + ".0")

			cfg.Default[developmentBranchName] = config
		}
	}

	// nonEOLNonGAVerion: Set config for {release/openshift}-$VERSION:
	// * dependentBugStatus: ["MODIFIED", "ON_QA", "VERIFIED"]
	// * DependentBugTargetReleases: "$VERSION+1.0"
	// * ValidateByDefault: true
	for _, nonEOLNonGAVerion := range nonEOLNonGAVersions {
		for _, releaseBranchPrefix := range releaseBranchPrefixes {
			cfg.Default[releaseBranchPrefix+nonEOLNonGAVerion.String()] = plugins.BugzillaBranchOptions{
				DependentBugStates:         developmentDependentBugStates,
				DependentBugTargetReleases: &[]string{nonEOLNonGAVerion.WithIncrementedMinor(1).String() + ".0"},
				TargetRelease:              utilpointer.String(nonEOLNonGAVerion.String() + ".0"),
				ValidateByDefault:          utilpointer.Bool(true),
			}
		}
	}

	// GA versions:
	// * DependentBugTargetReleases: "$VERSION+1.0"
	// * if not latestGA: add $Version+1.z to DependentBugTargetReleases
	// * TargetRelease: $VERSION.z
	// * ValidateByDefault: true
	sort.Slice(gaVersions, func(i, j int) bool { return gaVersions[i].Less(gaVersions[j]) })
	for idx, gaVersion := range gaVersions {
		isLatestGA := idx == len(gaVersions)-1

		dependentBugTargetReleases := []string{gaVersion.WithIncrementedMinor(1).String() + ".0"}
		if !isLatestGA {
			dependentBugTargetReleases = append(dependentBugTargetReleases, gaVersion.WithIncrementedMinor(1).String()+".z")
		}
		config := plugins.BugzillaBranchOptions{
			DependentBugTargetReleases: &dependentBugTargetReleases,
			TargetRelease:              utilpointer.String(gaVersion.String() + ".z"),
			ValidateByDefault:          utilpointer.Bool(true),
		}

		for _, releaseBranchPrefix := range releaseBranchPrefixes {
			cfg.Default[releaseBranchPrefix+gaVersion.String()] = config
		}
	}

	return cfg
}
