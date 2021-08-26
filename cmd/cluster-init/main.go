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

	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

const (
	githubOrg      = "openshift"
	githubRepo     = "release"
	githubLogin    = "openshift-bot"
	githubTeam     = "openshift/test-platform"
	upstreamBranch = "master"
)

type options struct {
	clusterName string
	releaseRepo string
	description string

	assign      string
	githubLogin string

	bumper.GitAuthorOptions
	prcreation.PRCreationOptions
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
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use. Set to "+githubLogin+" by default")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to. Set to Test Platform by default")

	o.GitAuthorOptions.AddFlags(fs)
	o.PRCreationOptions.AddFlags(fs)
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
	existsFor, err := periodicExistsFor(o)
	if existsFor || err != nil {
		errs = append(errs, fmt.Errorf("cluster: %s already exists", o.clusterName))
	}
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
	}
	if err = o.GitAuthorOptions.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err = o.PRCreationOptions.Validate(true); err != nil {
		errs = append(errs, err)
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

	//TODO: should we validate that the o.releaseRepo dir is "clean" before we start?

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	for _, step := range []func(options) error{
		updateInfraPeriodics,
		updatePostsubmits,
		updatePresubmits,
		updateClustersReadme,
		initClusterBuildFarmDir,
		updateCiSecretBootstrapConfig,
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
	} else {
		if err := submitPR(o); err != nil {
			logrus.WithError(err).Fatalf("couldn't commit changes")
		}
	}
}

func submitPR(o options) error {
	if err := o.PRCreationOptions.Finalize(); err != nil {
		logrus.WithError(err).Fatal("failed to finalize PR creation options")
	}
	if err := os.Chdir(o.releaseRepo); err != nil {
		return err
	}
	if err := exec.Command("git", "checkout", "-b", "init-"+o.clusterName).Run(); err != nil {
		return err
	}
	title := fmt.Sprintf("Initialize Build Cluster %s", o.clusterName)
	if err := o.PRCreationOptions.UpsertPR(o.releaseRepo,
		githubOrg,
		githubRepo,
		upstreamBranch,
		title,
		prcreation.PrAssignee(o.assign),
		prcreation.MatchTitle(title)); err != nil {
		return err
	}
	// We have to clean up the remote created by bumper in the UpsertPR method if we want to be able to run this again from the same repo
	return exec.Command("git", "remote", "rm", "bumper-fork-remote").Run()
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
			return fmt.Errorf("failed to symlink %s: %w", item, err)
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
