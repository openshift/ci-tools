package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

const (
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/test-platform"
	master      = "master"
)

type options struct {
	clusterName string
	releaseRepo string
	update      bool
	createPR    bool

	assign      string
	githubLogin string

	bumper.GitAuthorOptions
	prcreation.PRCreationOptions
}

func (o options) String() string {
	return fmt.Sprintf("%#v", o)
}

func parseOptions() (options, error) {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.BoolVar(&o.update, "update", false, "Run in update mode. Set to false by default")
	fs.BoolVar(&o.createPR, "create-pr", true, "If a PR should be created. Set to true by default")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use. Set to "+githubLogin+" by default")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to. Set to Test Platform by default")

	o.GitAuthorOptions.AddFlags(fs)
	o.PRCreationOptions.AddFlags(fs)
	return o, fs.Parse(os.Args[1:])
}

func validateOptions(o options) []error {
	var errs []error
	if !o.update && o.clusterName == "" {
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
		// If the release repo is missing, further checks won't be possible
		errs = append(errs, errors.New("--release-repo must be provided"))
	} else {
		if o.createPR {
			// make sure the release repo is on the master branch and clean
			if err := os.Chdir(o.releaseRepo); err != nil {
				errs = append(errs, err)
			} else {
				branch, err := exec.Command("git", "rev-parse", "--symbolic-full-name", "--abbrev-ref", "HEAD").Output()
				if err != nil {
					errs = append(errs, err)
				} else if master != strings.TrimSpace(string(branch)) {
					errs = append(errs, errors.New("--release-repo is not currently on master branch"))
				} else {
					hasChanges, err := bumper.HasChanges()
					if err != nil {
						errs = append(errs, err)
					}
					if hasChanges {
						errs = append(errs, errors.New("--release-repo has local changes"))
					}
				}
			}
		}

		if o.update {
			if o.clusterName != "" {
				buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
				if _, err := os.Stat(buildDir); os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("build farm directory: %s does not exist. Must exist to perform update", o.clusterName))
				}
			}
		} else {
			if o.clusterName != "" {
				buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
				if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
				}
			}
		}
	}
	if err := o.GitAuthorOptions.Validate(); err != nil {
		errs = append(errs, err)
	}
	if err := o.PRCreationOptions.Validate(true); err != nil {
		errs = append(errs, err)
	}
	return errs
}

const (
	buildUFarm                 = "build_farm"
	podScaler                  = "pod-scaler"
	configUpdater              = "config-updater"
	ciOperator                 = "ci-operator"
	buildFarm                  = "build-farm"
	githubLdapUserGroupCreator = "github-ldap-user-group-creator"
	promotedImageGovernor      = "promoted-image-governor"
	clusterDisplay             = "cluster-display"
	ci                         = "ci"
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
		var errorMessage string
		for _, err := range validationErrors {
			errorMessage += "\n" + err.Error()
		}
		logrus.Fatalf("validation errors: %v", errorMessage)
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	var clusters []string
	if o.clusterName == "" {
		// Updating ALL cluster-init managed clusters
		buildClusters, err := loadBuildClusters(o)
		if err != nil {
			logrus.WithError(err).Error("failed to obtain managed build clusters")
		}
		clusters = buildClusters.Managed
	} else {
		clusters = []string{o.clusterName}
	}
	for _, cluster := range clusters {
		o.clusterName = cluster
		steps := []func(options) error{
			updateJobs,
			updateClusterBuildFarmDir,
			updateCiSecretBootstrap,
			updateSecretGenerator,
			updateSanitizeProwJobs,
			updateSyncRoverGroups,
			updateProwPluginConfig,
		}
		if !o.update {
			steps = append(steps, updateBuildClusters)
		}
		for _, step := range steps {
			if err := step(o); err != nil {
				logrus.WithError(err).Error("failed to execute step")
				errorCount++
			}
		}
	}
	if errorCount > 0 {
		logrus.Fatalf("Due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	} else if o.createPR {
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
	branchName := "init-" + o.clusterName
	if err := exec.Command("git", "checkout", "-b", branchName).Run(); err != nil {
		return err
	}
	title := fmt.Sprintf("Initialize Build Cluster %s", o.clusterName)
	metadata := RepoMetadata()
	if err := o.PRCreationOptions.UpsertPR(o.releaseRepo,
		metadata.Org,
		metadata.Repo,
		metadata.Branch,
		title,
		prcreation.PrAssignee(o.assign),
		prcreation.MatchTitle(title)); err != nil {
		return err
	}
	// We have to clean up the remote created by bumper in the UpsertPR method if we want to be able to run this again from the same repo
	if err := exec.Command("git", "remote", "rm", "bumper-fork-remote").Run(); err != nil {
		return err
	}
	return exec.Command("git", "checkout", master).Run()
}

func updateClusterBuildFarmDir(o options) error {
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	if o.update {
		logrus.Infof("Updating build dir: %s", buildDir)
	} else {
		logrus.Infof("creating build dir: %s", buildDir)
		if err := os.MkdirAll(buildDir, 0777); err != nil {
			return fmt.Errorf("failed to create base directory for cluster: %w", err)
		}
	}
	for _, item := range []string{"common", "common_except_app.ci"} {
		target := fmt.Sprintf("../%s", item)
		source := filepath.Join(buildDir, item)
		if o.update {
			if err := os.RemoveAll(source); err != nil {
				return fmt.Errorf("failed to remove symlink %s, error: %w", source, err)
			}
		}
		if err := os.Symlink(target, source); err != nil {
			return fmt.Errorf("failed to symlink %s to ../%s", item, item)
		}
	}
	return nil
}

func buildFarmDirFor(releaseRepo, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", clusterName)
}

func serviceAccountKubeconfigPath(serviceAccount, clusterName string) string {
	return fmt.Sprintf("sa.%s.%s.config", serviceAccount, clusterName)
}
