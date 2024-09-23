package onboard

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/getlantern/deepcopy"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"

	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusterdir"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/buildclusters"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretbootstrap"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/cisecretgenerator"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/jobs"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/prowplugin"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/sanitizeprowjob"
	"github.com/openshift/ci-tools/cmd/cluster-init/cmd/onboard/syncrovergroup"
	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	"github.com/openshift/ci-tools/pkg/clustermgmt"
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

	*runtime.Options
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

func newGenerateConfigCmd(ctx context.Context, log *logrus.Entry,
	parentOpts *runtime.Options) *cobra.Command {
	opts.Options = parentOpts
	cmd := cobra.Command{
		Use:   "generate",
		Short: "Generate the configuration files for a new cluster",
		Long:  "Generate the configuration files for a new cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateConfig(ctx, log, runtime.ClusterInstallGetterFunc(opts.ClusterInstall))
		},
	}

	fs := cmd.PersistentFlags()
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

func generateConfig(ctx context.Context, log *logrus.Entry, clusterInstall clustermgmt.ClusterInstallGetter) error {
	log = log.WithField("stage", "onboard config")

	ci, err := clusterInstall()
	if err != nil {
		return fmt.Errorf("cluster install: %w", err)
	}

	opts.clusterName = ci.ClusterName
	opts.releaseRepo = ci.Onboard.ReleaseRepo
	opts.osd = *ci.Onboard.OSD
	opts.hosted = *ci.Onboard.Hosted
	opts.unmanaged = *ci.Onboard.Unmanaged

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

	buildClusters, err := buildclusters.LoadBuildClusters(ci)
	if err != nil {
		return fmt.Errorf("load build clusters: %w", err)
	}

	if ci.ClusterName == "" {
		clusters = buildClusters.Managed
	} else {
		clusters = []string{ci.ClusterName}
	}

	kubeconfigs, err := loadKubeconfigs(ci, opts.update)
	if err != nil {
		return fmt.Errorf("load kubeconfigs: %w", err)
	}

	osdSet := sets.New(buildClusters.Osd...)
	hostedSet := sets.New(buildClusters.Hosted...)
	for _, cluster := range clusters {
		steps := []func(log *logrus.Entry, ci *clustermgmt.ClusterInstall) error{
			jobs.UpdateJobs,
			buildclusterdir.UpdateClusterBuildFarmDir,
			func(log *logrus.Entry, ci *clustermgmt.ClusterInstall) error {
				return clustermgmtonboard.NewOAuthTemplateStep(log, ci).Run(ctx)
			},
			cisecretbootstrap.UpdateCiSecretBootstrap,
			cisecretgenerator.UpdateSecretGenerator,
			sanitizeprowjob.UpdateSanitizeProwJobs,
			syncrovergroup.UpdateSyncRoverGroups,
			prowplugin.UpdateProwPluginConfig,
			func(log *logrus.Entry, ci *clustermgmt.ClusterInstall) error {
				kubeClient := kubeClientFunc(kubeconfigs, ci, opts.update)
				dexStep := clustermgmtonboard.NewDexStep(log, kubeClient, ci)
				return dexStep.Run(ctx)
			},
		}
		if !opts.update {
			steps = append(steps, buildclusters.UpdateBuildClusters)
		}
		for _, step := range steps {
			ciCopy := clustermgmt.ClusterInstall{}
			if err := deepcopy.Copy(&ciCopy, ci); err != nil {
				return fmt.Errorf("cluster install deep copy: %w", err)
			}
			if ci.ClusterName == "" {
				ciCopy.ClusterName = cluster
				ciCopy.Onboard.OSD = ptr.To(osdSet.Has(cluster))
				ciCopy.Onboard.Hosted = ptr.To(hostedSet.Has(cluster))
			}
			if err := step(log, &ciCopy); err != nil {
				log.WithError(err).Error("failed to execute step")
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

func loadKubeconfigs(ci *clustermgmt.ClusterInstall, updateMode bool) (*runtime.Kubeconfigs, error) {
	adminKubeconfigPath := ""
	if !updateMode {
		adminKubeconfigPath = clustermgmtonboard.AdminKubeconfig(ci.InstallBase)
	}
	kubeconfigs, err := runtime.LoadKubeconfigs(ci.Onboard.KubeconfigDir, ci.Onboard.KubeconfigSuffix, adminKubeconfigPath)
	if err != nil {
		return nil, err
	}
	return kubeconfigs, nil
}

// kubeClientFunc return a function that resolves a kube client. When the cluster-init tool runs in "update"
// mode we pass it a bunch of kubeconfigs regarding clusters that have been created and onboarded already.
// On the other hand, when we are in the middle of creating one, we have to rely on the admin kubeconfig
// dropped by the openshit-install.
func kubeClientFunc(kubeconfigs *runtime.Kubeconfigs, ci *clustermgmt.ClusterInstall, updateMode bool) func() (ctrlruntimeclient.Client, error) {
	return func() (ctrlruntimeclient.Client, error) {
		var config *rest.Config
		if updateMode {
			c, found := kubeconfigs.Resolve(ci.ClusterName)
			if !found {
				return nil, fmt.Errorf("kubeconfig for %s not found", ci.ClusterName)
			}
			config = &c
		} else {
			c, found := kubeconfigs.Admin()
			if !found {
				return nil, fmt.Errorf("admin kubeconfig for %s not found", ci.ClusterName)
			}
			config = &c
		}
		return ctrlruntimeclient.New(config, ctrlruntimeclient.Options{})
	}
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
