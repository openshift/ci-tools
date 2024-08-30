package onboard

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusterdir"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusters"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretbootstrap"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretgenerator"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/jobs"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/prowplugin"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/sanitizeprowjob"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/syncrovergroup"
	clustermgmtonboard "github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

const (
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/test-platform"
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
				} else if clustermgmtonboard.Master != strings.TrimSpace(string(branch)) {
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
				buildDir := clustermgmtonboard.BuildFarmDirFor(o.releaseRepo, o.clusterName)
				if _, err := os.Stat(buildDir); os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("build farm directory: %s does not exist. Must exist to perform update", o.clusterName))
				}
			}
		} else {
			if o.clusterName != "" {
				buildDir := clustermgmtonboard.BuildFarmDirFor(o.releaseRepo, o.clusterName)
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

func newConfigCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:   "config [generate|apply]",
		Short: "Onboard a cluster",
		Long:  "Handle the onboarding procedure by generate the required yamls and secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}

			switch args[0] {
			case "generate":
				generateConfig()
			case "apply":
				return errors.New("onboard config apply not implemented yet")
			default:
				return fmt.Errorf("action %q is not supported", args[0])
			}

			return nil
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

func generateConfig() error {
	validationErrors := validateOptions(opts)
	if len(validationErrors) > 0 {
		var errorMessage string
		for _, err := range validationErrors {
			errorMessage += "\n" + err.Error()
		}
		return fmt.Errorf("validation errors: %s", errorMessage)
	}

	// Each step in the process is allowed to fail independently so that the diffs for the others can still be generated
	errorCount := 0
	var clusters []string
	var hostedClusters []string
	var osdClusters []string

	buildClusters, err := buildclusters.LoadBuildClusters(buildclusters.Options{
		ClusterName: opts.clusterName,
		ReleaseRepo: opts.releaseRepo,
		Unmanaged:   opts.unmanaged,
		OSD:         opts.osd,
		Hosted:      opts.hosted,
	})
	if err != nil {
		return fmt.Errorf("load build cluster: %w", err)
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
			func(o options) error {
				return jobs.UpdateJobs(jobs.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
					Unmanaged:   o.unmanaged,
				}, osdClusters)
			},
			func(o options) error {
				return buildclusterdir.UpdateClusterBuildFarmDir(buildclusterdir.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
					Update:      o.update,
				}, hostedClusters)
			},
			func(o options) error {
				return cisecretbootstrap.UpdateCiSecretBootstrap(cisecretbootstrap.Options{
					ClusterName:              o.clusterName,
					ReleaseRepo:              o.releaseRepo,
					UseTokenFileInKubeconfig: o.useTokenFileInKubeconfig,
					Unmanaged:                o.unmanaged,
				}, osdClusters)
			},
			func(o options) error {
				return cisecretgenerator.UpdateSecretGenerator(cisecretgenerator.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
					Unmanaged:   o.unmanaged,
				})
			},
			func(o options) error {
				return sanitizeprowjob.UpdateSanitizeProwJobs(sanitizeprowjob.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
				})
			},
			func(o options) error {
				return syncrovergroup.UpdateSyncRoverGroups(syncrovergroup.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
				})
			},
			func(o options) error {
				return prowplugin.UpdateProwPluginConfig(prowplugin.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
				})
			},
		}
		if !opts.update {
			steps = append(steps, func(o options) error {
				return buildclusters.UpdateBuildClusters(buildclusters.Options{
					ClusterName: o.clusterName,
					ReleaseRepo: o.releaseRepo,
					Unmanaged:   o.unmanaged,
					OSD:         o.osd,
					Hosted:      o.hosted,
				})
			})
		}
		for _, step := range steps {
			if err := step(opts); err != nil {
				logrus.WithError(err).Error("failed to execute step")
				errorCount++
			}
		}
	}
	if errorCount > 0 {
		return fmt.Errorf("due to the %d error(s) encountered a PR will not be generated. The resulting files can be PR'd manually", errorCount)
	} else if opts.createPR {
		if err := submitPR(opts); err != nil {
			return fmt.Errorf("submit PR: %w", err)
		}
	}

	return nil
}

func submitPR(o options) error {
	if err := o.PRCreationOptions.Finalize(); err != nil {
		return fmt.Errorf("finalize PR: %w", err)
	}
	if err := os.Chdir(o.releaseRepo); err != nil {
		return err
	}
	branchName := "init-" + o.clusterName
	if err := exec.Command("git", "checkout", "-b", branchName).Run(); err != nil {
		return err
	}
	title := fmt.Sprintf("Initialize Build Cluster %s", o.clusterName)
	metadata := clustermgmtonboard.RepoMetadata()
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
	return exec.Command("git", "checkout", clustermgmtonboard.Master).Run()
}
