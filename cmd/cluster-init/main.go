package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

type options struct {
	clusterName string
	releaseRepo string
}

func (o options) String() string {
	return fmt.Sprintf("cluster-name: %s\nrelease-repo: %s",
		o.clusterName,
		o.releaseRepo)
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("cannot parse args: ", os.Args[1:])
	}
	return o
}

func validateOptions(o options) []error {
	var errs []error
	if o.clusterName == "" {
		errs = append(errs, fmt.Errorf("--cluster-name must be provided"))
	} else if strings.ContainsAny(o.clusterName, " \t\n") {
		errs = append(errs, fmt.Errorf("--cluster-name must not contain whitespace"))
	}
	if o.releaseRepo == "" {
		errs = append(errs, fmt.Errorf("--release-repo must be provided"))
	}
	if o.clusterName != "" {
		buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
		if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
		}
	}
	return errs
}

const (
	BuildUFarm = "build_farm"
	PodScaler  = "pod-scaler"
)

func main() {
	o := parseOptions()
	validationErrors := validateOptions(o)
	if len(validationErrors) > 0 {
		logrus.Fatalf("validation errors: %v", validationErrors)
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	for _, step := range []func(options) error{
		updateClustersReadme,
		initClusterBuildFarmDir,
		updateSecretGenerator,
		updateSanitizeProwJobs,
	} {
		if err := step(o); err != nil {
			logrus.WithError(err)
			errorCount++
		}
	}
	if errorCount > 0 {
		logrus.Infof("Due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	}
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
	logrus.Infof("Creating build dir: %s\n", buildDir)
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
