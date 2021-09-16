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

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
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
	createPR    bool

	assign      string
	githubLogin string

	bumper.GitAuthorOptions
	prcreation.PRCreationOptions
}

func (o options) secretBootstrapConfigFile() string {
	return filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap", "_config.yaml")
}

func (o options) String() string {
	return fmt.Sprintf("%#v", o)
}

func parseOptions() (options, error) {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&o.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.BoolVar(&o.createPR, "create-pr", true, "If a PR should be created. Set to true by default")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use. Set to "+githubLogin+" by default")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to. Set to Test Platform by default")

	o.GitAuthorOptions.AddFlags(fs)
	o.PRCreationOptions.AddFlags(fs)
	return o, fs.Parse(os.Args[1:])
}

func validateOptions(o options) []error {
	var errs []error
	if o.clusterName != "" {
		for _, char := range o.clusterName {
			if unicode.IsSpace(char) {
				errs = append(errs, errors.New("--cluster-name must not contain whitespace"))
				break
			}
		}
	}
	if o.releaseRepo == "" {
		//If the release repo is missing, further checks won't be possible
		errs = append(errs, errors.New("--release-repo must be provided"))
	} else {
		if o.createPR {
			//make sure the release repo is on the master branch and clean
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

		if o.clusterName != "" {
			buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
			if _, err := os.Stat(buildDir); !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("build farm directory: %s already exists", o.clusterName))
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
	buildUFarm    = "build_farm"
	podScaler     = "pod-scaler"
	configUpdater = "config-updater"
	ciOperator    = "ci-operator"
	buildFarm     = "build-farm"
	ci            = "ci"
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

	if o.clusterName == "" {
		logrus.Infof("validating configurations for the existing clusters")
		clusters, err := getClusters(o)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to get any build clusters")
		}
		var errs []error
		for _, step := range []func(options, []string) error{
			validateJobs,
			validateClusterBuildFarmDir,
		} {
			if err := step(o, clusters); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			logrus.WithError(kerrors.NewAggregate(errs)).Fatal("Failed to validate the build farm configurations")
		}
		return
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	for _, step := range []func(options) error{
		updateJobs,
		initClusterBuildFarmDir,
		updateCiSecretBootstrap,
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
	} else if o.createPR {
		if err := submitPR(o); err != nil {
			logrus.WithError(err).Fatalf("couldn't commit changes")
		}
	}
}

func getClusters(o options) ([]string, error) {
	secretBootstrapConfigFile := o.secretBootstrapConfigFile()
	var c secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(secretBootstrapConfigFile, &c); err != nil {
		return nil, err
	}
	ret := c.ClusterGroups[nonAppCiX86Group]
	if len(ret) == 0 {
		return nil, fmt.Errorf(".cluster_groups[non_app_ci_x86] is empty in ci-secret-bootstrap's config file: %q", secretBootstrapConfigFile)
	}
	return ret, nil
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

func validateClusterBuildFarmDir(o options, clusters []string) error {
	for _, cluster := range clusters {
		buildDir := buildFarmDirFor(o.releaseRepo, cluster)
		logrus.Infof("Validating build dir: %s", buildDir)
		fileInfo, err := os.Stat(buildDir)
		if err != nil {
			return err
		}
		if !fileInfo.IsDir() {
			return fmt.Errorf("%s is not a directory", buildDir)
		}

		for _, item := range []string{"common", "common_except_app.ci"} {
			newName := filepath.Join(buildDir, item)
			newNameFileInfo, err := os.Lstat(newName)
			if err != nil {
				return err
			}
			if newNameFileInfo.Mode()&os.ModeSymlink != 0 {
				oldName, err := filepath.EvalSymlinks(newName)
				if err != nil {
					return err
				}
				expectedOldName := filepath.Join(filepath.Dir(buildDir), item)
				if oldName != expectedOldName {
					return fmt.Errorf("the resolved target is %s,  expecting %s", oldName, expectedOldName)
				}
				oldNameFileInfo, err := os.Stat(oldName)
				if err != nil {
					return err
				}
				if !oldNameFileInfo.IsDir() {
					return fmt.Errorf("the linked file is not a directory: %s", oldName)
				}
			} else {
				return fmt.Errorf(" %s is not a symlink", newName)
			}
		}
	}
	return nil
}

func buildFarmDirFor(releaseRepo string, clusterName string) string {
	return filepath.Join(releaseRepo, "clusters", "build-clusters", folder(clusterName))
}

func folder(name string) string {
	switch name {
	case string(api.ClusterBuild01):
		return "01_cluster"
	case string(api.ClusterBuild02):
		return "02_cluster"
	default:
		return name
	}
}

func serviceAccountKubeconfigPath(serviceAccount, clusterName string) string {
	return fmt.Sprintf("sa.%s.%s.config", serviceAccount, clusterName)
}
