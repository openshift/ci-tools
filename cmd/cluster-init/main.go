package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"unicode"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

type options struct {
	clusterName string
	releaseRepo string
}

func (o options) String() string {
	return fmt.Sprintf("%#v", o)
}

func parseOptions() (options, error) {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")

	return o, fs.Parse(os.Args[1:])
}

func validateOptions(o options) []error {
	var errs []error
	if o.clusterName == "" {
		errs = append(errs, errors.New("--cluster-name must be provided"))
	} else {
		for _, char := range o.clusterName {
			if unicode.IsSpace(char) {
				errs = append(errs, errors.New("--cluster-name must not contain whitespace"))
				break
			}
		}
	}
	if o.releaseRepo == "" {
		//If the release repo is missing, further checks won't be possible
		return append(errs, errors.New("--release-repo must be provided"))
	}
	if o.clusterName != "" {
		existsFor, err := periodicExistsFor(o)
		if err != nil {
			errs = append(errs, err)
		}
		if existsFor {
			errs = append(errs, fmt.Errorf("cluster: %s already exists", o.clusterName))
		}
		buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
		if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
		}
	}
	return errs
}

const (
	BuildUFarm    = "build_farm"
	PodScaler     = "pod-scaler"
	ConfigUpdater = "config-updater"
)

func RepoMetadata() *api.Metadata {
	return &api.Metadata{
		Org:    "openshift",
		Repo:   "release",
		Branch: "master",
	}
}

func main() {
	o, err := parseOptions()
	if err != nil {
		logrus.WithError(err).Fatal("cannot parse args: ", os.Args[1:])
	}
	validationErrors := validateOptions(o)
	if len(validationErrors) > 0 {
		logrus.Fatalf("validation errors: %v", validationErrors)
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	for _, step := range []func(options) error{
		updateInfraPeriodics,
		updatePostsubmits,
		updatePresubmits,
		initClusterBuildFarmDir,
		updateSecretGenerator,
		updateSanitizeProwJobs,
	} {
		if err := step(o); err != nil {
			logrus.WithError(err).Error("failed to execute step")
			errorCount++
		}
	}
	if errorCount > 0 {
		logrus.Fatalf("Due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	}
}

func initClusterBuildFarmDir(o options) error {
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	logrus.Infof("Creating build dir: %s", buildDir)
	if err := os.MkdirAll(buildDir, 0777); err != nil {
		return fmt.Errorf("failed to create base directory for cluster: %w", err)
	}

	for _, item := range []string{"common", "common_except_app.ci"} {
		if err := os.Symlink(fmt.Sprintf("../%s", item), filepath.Join(buildDir, item)); err != nil {
			return fmt.Errorf("failed to symlink %s to ../%s", item, item)
		}
	}
	return nil
}

func buildFarmDirFor(releaseRepo string, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName)
}

func serviceAccountKubeconfigPath(serviceAccount, clusterName string) string {
	return fmt.Sprintf("sa.%s.%s.config", serviceAccount, clusterName)
}
