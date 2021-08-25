package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	errorCount := 0
	completeStep(updateInfraPeriodics, o, &errorCount)
	completeStep(updatePostsubmits, o, &errorCount)
	completeStep(updatePresubmits, o, &errorCount)
	completeStep(updateClustersReadme, o, &errorCount)
	completeStep(initClusterBuildFarmDir, o, &errorCount)
	completeStep(updateCiSecretBootstrapConfig, o, &errorCount)
	completeStep(updateSecretGenerator, o, &errorCount)
	completeStep(updateSanitizeProwJobs, o, &errorCount)
	if errorCount > 0 {
		logrus.Printf("Due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	} else {
		if err := submitPR(o); err != nil {
			logrus.WithError(err).Fatalf("couldn't commit changes")
		}
	}
}

func completeStep(stepFunction func(options) error, o options, errorCount *int) {
	if err := stepFunction(o); err != nil {
		logrus.WithError(err).Log(logrus.ErrorLevel, "failed to update sanitize-prow-jobs config")
		*errorCount++
	}
}

func submitPR(o options) error {
	//TODO
	return nil
}

func updateClustersReadme(o options) error {
	reader := bufio.NewReader(os.Stdin)
	clustersReadmeFile := o.releaseRepo + "/clusters/README.md"
	fmt.Printf("Would you like to add information about the '%s' cluster to %s? [y,n]: ",
		o.clusterName, clustersReadmeFile)
	char, _, err := reader.ReadRune()
	if err != nil {
		return err
	}
	switch char {
	case 'y':
		cmd := exec.Command("vim", clustersReadmeFile)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		return cmd.Run()
	default:
		return nil
	}
}

func initClusterBuildFarmDir(o options) error {
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	logrus.Printf("Creating build dir: %s\n", buildDir)
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
