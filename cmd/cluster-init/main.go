package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

type options struct {
	clusterName string
	releaseRepo string
	description string

	//flagutil.GitHubOptions TODO: this will come in later I think...lets ignore github stuff for now
}

func (o options) String() string {
	return fmt.Sprintf("cluster-name: %s\nrelease-repo: %s\ndescription: %s",
		o.clusterName,
		o.releaseRepo,
		o.description)
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.StringVar(&o.description, "description", "", "This clusters description to be used in the README.MD.")
	//o.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("cannot parse args: ", os.Args[1:])
	}
	return o
}

func validateOptions(o options) []error {
	var errs []error
	if o.clusterName == "" {
		errs = append(errs, fmt.Errorf("--cluster-name must be provided"))
	}
	if o.releaseRepo == "" {
		errs = append(errs, fmt.Errorf("--release-repo must be provided"))
	}
	if o.description == "" {
		errs = append(errs, fmt.Errorf("--description must be provided"))
	}
	existsFor, err := periodicExistsFor(o)
	if existsFor || err != nil {
		errs = append(errs, fmt.Errorf("cluster: %s already exists", o.clusterName))
	}
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
	}
	return errs
}

const (
	CiOperator    = "ci-operator"
	Kubeconfig    = "KUBECONFIG"
	ConfigUpdater = "config-updater"
	Config        = "config"
)

func main() {
	o := parseOptions()
	validationErrors := validateOptions(o)
	if len(validationErrors) > 0 {
		logrus.Fatalf("validation errors: %v", validationErrors)
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	if err := updateInfraPeriodics(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update Periodic Jobs")
	}
	if err := updatePostsubmits(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update PostSubmit Jobs")
	}
	if err := updatePresubmits(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update PreSubmit Jobs")
	}
	//TODO: is the following good enough? it is hard to modify MD programmatically
	fmt.Printf("Please add information about the '%s' cluster to %s/clusters/README.md\n",
		o.clusterName, o.releaseRepo)
	if err := initClusterBuildFarmDir(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to initialize build farm dir")
	}
	if err := updateCiSecretBootstrapConfig(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update ci-secretbootstrap config")
	}
	if err := updateSecretGenerator(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update ci-secretgenerator config")
	}
	if err := updateSanitizeProwJobs(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update sanitize-prow-jobs config")
	}
}

func initClusterBuildFarmDir(o options) error {
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	fmt.Printf("Creating build dir: %s\n", buildDir)
	if err := os.MkdirAll(buildDir, 0777); err != nil {
		return fmt.Errorf("failed to create base directory for cluster: %w", err)
	}

	for _, item := range []string{"common", "common_except_app.ci"} {
		if err := os.Symlink(fmt.Sprintf("../%s", item), filepath.Join(buildDir, item)); err != nil {
			return fmt.Errorf("failed to symlink %s: %w", item, err)
		}
	}
	return nil
}

func buildFarmDirFor(releaseRepo string, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName)
}

func secretConfigFor(secret string, clusterName string) string {
	return fmt.Sprintf("sa.%s.%s.config", secret, clusterName)
}
