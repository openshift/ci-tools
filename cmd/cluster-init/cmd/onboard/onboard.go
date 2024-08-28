package onboard

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
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretbootstrap"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

const (
	githubLogin                = "openshift-bot"
	githubTeam                 = "openshift/test-platform"
	master                     = "master"
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

var (
	opts = options{}
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

	useTokenFileInKubeconfig bool

	hosted    bool
	unmanaged bool
	osd       bool
}

func (o options) String() string {
	return fmt.Sprintf("%#v", o)
}

func New() *cobra.Command {
	cmd := cobra.Command{
		Use:   "cluster-init",
		Short: "cluster-init manages a TP cluster lifecycle",
		Long:  "A tool to provision, onboard and deprovision a TP cluster",
		Run: func(cmd *cobra.Command, args []string) {
			onboard()
		},
	}

	fs := cmd.PersistentFlags()
	fs.StringVar(&opts.clusterName, "cluster-name", "", "The name of the new cluster.")
	fs.StringVar(&opts.releaseRepo, "release-repo", "", "Path to the root of the openshift/release repository.")
	fs.BoolVar(&opts.update, "update", false, "Run in update mode. Set to false by default")
	fs.BoolVar(&opts.createPR, "create-pr", true, "If a PR should be created. Set to true by default")
	fs.StringVar(&opts.githubLogin, "github-login", githubLogin, "The GitHub username to use. Set to "+githubLogin+" by default")
	fs.StringVar(&opts.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to. Set to Test Platform by default")
	fs.BoolVar(&opts.useTokenFileInKubeconfig, "use-token-file-in-kubeconfig", true, "Set true if the token files are used in kubeconfigs. Set to true by default")
	fs.BoolVar(&opts.hosted, "hosted", false, "Set true if the cluster is hosted (i.e., HyperShift hosted cluster). Set to false by default")
	fs.BoolVar(&opts.unmanaged, "unmanaged", false, "Set true if the cluster is unmanaged (i.e., not managed by DPTP). Set to false by default")
	fs.BoolVar(&opts.osd, "osd", true, "Set true if the cluster is an OSD cluster. Set to true by default")

	stdFs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.GitAuthorOptions.AddFlags(stdFs)
	opts.PRCreationOptions.AddFlags(stdFs)
	fs.AddGoFlagSet(stdFs)

	return &cmd
}

func onboard() {
	validationErrors := validateOptions(opts)
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
	var hostedClusters []string
	var osdClusters []string

	buildClusters, err := loadBuildClusters(opts)
	if err != nil {
		logrus.WithError(err).Error("failed to obtain managed build clusters")
	}

	if opts.clusterName == "" {
		// Updating ALL cluster-init managed clusters
		clusters = buildClusters.Managed
		hostedClusters = buildClusters.Hosted
		osdClusters = buildClusters.Osd
	} else {
		clusters = []string{opts.clusterName}
		if opts.hosted {
			hostedClusters = append(buildClusters.Hosted, opts.clusterName)
		}
		if opts.osd {
			osdClusters = append(buildClusters.Osd, opts.clusterName)
		}
	}

	for _, cluster := range clusters {
		opts.clusterName = cluster
		steps := []func(options) error{
			func(o options) error { return updateJobs(o, osdClusters) },
			func(o options) error { return updateClusterBuildFarmDir(o, hostedClusters) },
			func(o options) error {
				return cisecretbootstrap.UpdateCiSecretBootstrap(cisecretbootstrap.Options{
					ClusterName:              o.clusterName,
					ReleaseRepo:              o.releaseRepo,
					UseTokenFileInKubeconfig: o.useTokenFileInKubeconfig,
					Unmanaged:                o.unmanaged,
				}, osdClusters)
			},
			updateSecretGenerator,
			updateSanitizeProwJobs,
			updateSyncRoverGroups,
			updateProwPluginConfig,
		}
		if !opts.update {
			steps = append(steps, updateBuildClusters)
		}
		for _, step := range steps {
			if err := step(opts); err != nil {
				logrus.WithError(err).Error("failed to execute step")
				errorCount++
			}
		}
	}
	if errorCount > 0 {
		logrus.Fatalf("Due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	} else if opts.createPR {
		if err := submitPR(opts); err != nil {
			logrus.WithError(err).Fatalf("couldn't commit changes")
		}
	}
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

func repoMetadata() *api.Metadata {
	return &api.Metadata{
		Org:    "openshift",
		Repo:   "release",
		Branch: "master",
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
	metadata := repoMetadata()
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

func updateClusterBuildFarmDir(o options, hostedClusters []string) error {
	buildDir := buildFarmDirFor(o.releaseRepo, o.clusterName)
	if o.update {
		logrus.Infof("Updating build dir: %s", buildDir)
	} else {
		logrus.Infof("creating build dir: %s", buildDir)
		if err := os.MkdirAll(buildDir, 0777); err != nil {
			return fmt.Errorf("failed to create base directory for cluster: %w", err)
		}
	}

	config_dirs := []string{
		"common",
		"common_except_app.ci",
	}

	hostedClustersSet := sets.New[string](hostedClusters...)
	if !hostedClustersSet.Has(o.clusterName) {
		config_dirs = append(config_dirs, "common_except_hosted")
	}

	for _, item := range config_dirs {
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
	return serviceAccountFile(serviceAccount, clusterName, cisecretbootstrap.Config)
}

func serviceAccountTokenFile(serviceAccount, clusterName string) string {
	return serviceAccountFile(serviceAccount, clusterName, "token.txt")
}

func serviceAccountFile(serviceAccount, clusterName, fileType string) string {
	return fmt.Sprintf("sa.%s.%s.%s", serviceAccount, clusterName, fileType)
}
