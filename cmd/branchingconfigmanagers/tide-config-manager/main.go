package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	prowConfigDir            string
	shardedProwConfigBaseDir string
	lifecyclePhase           string
	excludedReposFile        string
	currentOCPVersion        string
}

const (
	branching              = "branching"
	preGeneralAvailability = "pre-general-availability"
	GeneralAvailability    = "general-availability"
	staffEngApproved       = "staff-eng-approved"
	cherryPickApproved     = "cherry-pick-approved"
	backportRiskAssessed   = "backport-risk-assessed"
	qeApproved             = "qe-approved"
	docsApproved           = "docs-approved"
	pxApproved             = "px-approved"
	validBug               = "bugzilla/valid-bug"
	release                = "release-"
	openshift              = "openshift-"
	mainBranch             = "main"
	masterBranch           = "master"
)

func gatherOptions() (*options, error) {
	o := &options{}
	var errs []error

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	fs.StringVar(&o.shardedProwConfigBaseDir, "sharded-prow-config-base-dir", "", "Basedir for the sharded prow config. If set, org and repo-specific config will get removed from the main prow config and written out in an org/repo tree below the base dir.")
	fs.StringVar(&o.lifecyclePhase, "lifecycle-phase", "", "Lifecycle phase, one of: branching, pre-general-availability, general-availability")
	fs.StringVar(&o.currentOCPVersion, "current-release", "", "Current OCP version")
	fs.StringVar(&o.excludedReposFile, "excluded-repos-config", "", "Path to the GA's excluded repos config file.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		errs = append(errs, fmt.Errorf("couldn't parse arguments: %w", err))
	}
	if o.lifecyclePhase != branching && o.lifecyclePhase != preGeneralAvailability && o.lifecyclePhase != GeneralAvailability {
		errs = append(errs, errors.New("--lifecycle-phase is required and has to be one of: branching, pre-general-availability, general-availability"))
	}
	if o.prowConfigDir == "" {
		errs = append(errs, errors.New("--prow-config-dir is required"))
	}
	if o.shardedProwConfigBaseDir == "" {
		errs = append(errs, errors.New("--sharded-prow-config-base-dir is required"))
	}
	if o.currentOCPVersion == "" {
		errs = append(errs, errors.New("--current-release is required"))
	}

	if _, err := ocplifecycle.ParseMajorMinor(o.currentOCPVersion); o.currentOCPVersion != "" && err != nil {
		errs = append(errs, fmt.Errorf("error parsing current-release %s", o.currentOCPVersion))
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	if err := updateProwConfigs(o); err != nil {
		logrus.WithError(err).Fatal("could not update Prow configuration")
	}
}

type sharedDataDelegate struct {
	excludedLabels sets.String
	mainMaster     sets.String
	validBug       sets.String
}

func newSharedDataDelegate() *sharedDataDelegate {
	return &sharedDataDelegate{
		excludedLabels: sets.NewString(qeApproved, docsApproved, pxApproved),
		mainMaster:     sets.NewString(mainBranch, masterBranch),
		validBug:       sets.NewString(validBug),
	}

}

type branchingDayEvent struct {
	repos                    sets.String
	openshiftReleaseBranches sets.String
	*sharedDataDelegate
}

func newBranchingDayEvent(current string, delegate *sharedDataDelegate) branchingDayEvent {
	return branchingDayEvent{
		repos:                    sets.NewString(),
		openshiftReleaseBranches: sets.NewString(release+current, openshift+current),
		sharedDataDelegate:       delegate,
	}
}

func (bde branchingDayEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)

	if branches.Intersection(bde.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(staffEngApproved) {
			reqLabels.Delete(staffEngApproved)
			reqLabels.Insert(cherryPickApproved, backportRiskAssessed)
		}
	}
	q.Labels = reqLabels.List()
}

func (bde branchingDayEvent) GetDataFromProwConfig(pc *prowconfig.ProwConfig) {
}

type preGeneralAvailabilityEvent struct {
	repos                    sets.String
	openshiftReleaseBranches sets.String
	*sharedDataDelegate
}

func newPreGeneralAvailability(current string, delegate *sharedDataDelegate) preGeneralAvailabilityEvent {
	return preGeneralAvailabilityEvent{
		repos:                    sets.NewString(),
		openshiftReleaseBranches: sets.NewString(release+current, openshift+current),
		sharedDataDelegate:       delegate,
	}
}

func (pga preGeneralAvailabilityEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)

	if branches.Intersection(pga.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(cherryPickApproved) && reqLabels.Has(backportRiskAssessed) {
			reqLabels.Insert(staffEngApproved)
		}
	}
	q.Labels = reqLabels.List()
}

func (pga preGeneralAvailabilityEvent) GetDataFromProwConfig(pc *prowconfig.ProwConfig) {
}

type excludedRepos struct {
	NoXYAllowList     []string `yaml:"NoXYAllowList,flow"`
	ExcludedAllowList []string `yaml:"ExcludedAllowList,flow"`
}

func (er *excludedRepos) loadExcludedReposConfig(path string) error {
	cfgBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read excluded repos config from path %s: %w", path, err)
	}
	if err := yaml.Unmarshal(cfgBytes, er); err != nil {
		return fmt.Errorf("failed to deserialize the excluded repos config: %w", err)
	}
	return nil
}

type generalAvailabilityEvent struct {
	repos                         sets.String
	excludedAllowList             sets.String
	noXYAllowList                 sets.String
	openshiftReleaseBranches      sets.String
	openshiftReleaseBranchesPlus1 sets.String
	releasePast                   string
	openshiftPast                 string
	releaseCurrent                string
	openshiftCurrent              string
	releaseFuture                 string
	openshiftFuture               string
	past                          string
	current                       string
	future                        string
}

func newGeneralAvailabilityEvent(past, current, future string, repos excludedRepos) generalAvailabilityEvent {
	noXYAllowList := sets.NewString(repos.NoXYAllowList...)
	excludedAllowList := sets.NewString(repos.ExcludedAllowList...).Union(noXYAllowList)

	return generalAvailabilityEvent{
		repos:                         sets.NewString(),
		excludedAllowList:             excludedAllowList,
		noXYAllowList:                 noXYAllowList,
		openshiftReleaseBranches:      sets.NewString(release+current, openshift+current),
		openshiftReleaseBranchesPlus1: sets.NewString(release+future, openshift+future),
		releasePast:                   release + past,
		releaseCurrent:                release + current,
		releaseFuture:                 release + future,
		openshiftPast:                 openshift + past,
		openshiftCurrent:              openshift + current,
		openshiftFuture:               openshift + future,
		past:                          past,
		current:                       current,
		future:                        future,
	}
}

func (gae generalAvailabilityEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	gae.deleteCherryPickApprovedBackportRiskAssesedLabels(q)
	gae.ensureStaffEngApprovedLabel(q)
	gae.ensureCherryPickApprovedLabel(q)
	gae.overrideExcludedBranches(q)
}

func (gae generalAvailabilityEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {}

func (gae *generalAvailabilityEvent) deleteCherryPickApprovedBackportRiskAssesedLabels(q *prowconfig.TideQuery) {
	reqLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)

	if branches.Intersection(gae.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(cherryPickApproved) && reqLabels.Has(backportRiskAssessed) && reqLabels.Has(staffEngApproved) {
			reqLabels.Delete(cherryPickApproved, backportRiskAssessed)
		}
	}
	q.Labels = reqLabels.List()
}

func (gae *generalAvailabilityEvent) ensureCherryPickApprovedLabel(q *prowconfig.TideQuery) {
	reqLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)
	if reqLabels.Has(cherryPickApproved) {
		if branches.Has(gae.releasePast) {
			branches.Insert(gae.releaseCurrent)
		}
		if branches.Has(gae.openshiftPast) {
			branches.Insert(gae.openshiftCurrent)
		}
		if branches.Intersection(gae.openshiftReleaseBranches).Len() == 0 && !gae.noXYAllowList.Has(q.Repos[0]) {
			fmt.Printf("Suspicious cherry-pick-approved query (without %s): %s\n", gae.current, q.Repos)
		}
		if branches.Intersection(gae.openshiftReleaseBranchesPlus1).Len() != 0 {
			fmt.Printf("Suspicious cherry-pick-approved query (with %s): %s\n", gae.future, q.Repos)
		}
	}
	q.IncludedBranches = branches.List()
}

func (gae *generalAvailabilityEvent) ensureStaffEngApprovedLabel(q *prowconfig.TideQuery) {
	reqLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)

	if reqLabels.Has(staffEngApproved) {
		if branches.Has(gae.releaseCurrent) {
			branches.Delete(gae.releaseCurrent)
			branches.Insert(gae.releaseFuture)
		}
		if branches.Has(gae.openshiftCurrent) {
			branches.Delete(gae.openshiftCurrent)
			branches.Insert(gae.openshiftFuture)
		}

		if !(branches.Equal(sets.NewString(gae.releaseFuture)) || branches.Equal(sets.NewString(gae.openshiftFuture)) || branches.Equal(gae.openshiftReleaseBranchesPlus1)) {
			fmt.Printf("Suspicious staff-eng-approved query: %s\n", q.Repos)
		}
	}
	q.IncludedBranches = branches.List()
}

func (gae *generalAvailabilityEvent) overrideExcludedBranches(q *prowconfig.TideQuery) {
	branches := sets.NewString(q.ExcludedBranches...)
	if branches.Has(gae.releasePast) {
		branches.Insert(gae.releaseCurrent)
		branches.Insert(gae.releaseFuture)
	}
	if branches.Has(gae.openshiftPast) {
		branches.Insert(gae.openshiftCurrent)
		branches.Insert(gae.openshiftFuture)
	}
	if branches.Len() > 0 {
		if branches.Intersection(gae.openshiftReleaseBranchesPlus1).Len() == 0 && !gae.excludedAllowList.Has(q.Repos[0]) {
			fmt.Printf("Suspicious complement query (without %s): %s\n", gae.future, q.Repos)
		}
	}
	q.ExcludedBranches = branches.List()
}

func updateProwConfigs(o *options) error {
	configPath := path.Join(o.prowConfigDir, config.ProwConfigFile)
	var additionalConfigs []string
	additionalConfigs = append(additionalConfigs, o.shardedProwConfigBaseDir)

	config, err := prowconfig.LoadStrict(configPath, "", additionalConfigs, "_prowconfig.yaml")
	if err != nil {
		return fmt.Errorf("failed to load Prow config in strict mode: %w", err)
	}

	repos := excludedRepos{}
	if o.excludedReposFile != "" {
		if err = repos.loadExcludedReposConfig(o.excludedReposFile); err != nil {
			return fmt.Errorf("failed to load the excluded repos configuration: %w", err)
		}
	}
	return reconcile(o.currentOCPVersion, o.lifecyclePhase, &config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), o.shardedProwConfigBaseDir), repos)
}

func reconcile(currentOCPVersion, lifecyclePhase string, config *prowconfig.ProwConfig, target afero.Fs, repos excludedRepos) error {
	delegate := newSharedDataDelegate()
	currentVersion, err := ocplifecycle.ParseMajorMinor(currentOCPVersion)
	if err != nil {
		return fmt.Errorf("failed to parse %s as majorMinor version: %w", currentOCPVersion, err)
	}
	if lifecyclePhase == branching {
		_, err = shardprowconfig.ShardProwConfig(config, target,
			newBranchingDayEvent(
				currentVersion.GetVersion(),
				delegate),
		)
	}
	if lifecyclePhase == preGeneralAvailability {
		_, err = shardprowconfig.ShardProwConfig(config, target,
			newPreGeneralAvailability(
				currentVersion.GetVersion(),
				delegate),
		)
	}
	if lifecyclePhase == GeneralAvailability {
		_, err = shardprowconfig.ShardProwConfig(config, target,
			newGeneralAvailabilityEvent(
				currentVersion.GetPastVersion(),
				currentVersion.GetVersion(),
				currentVersion.GetFutureVersion(),
				repos),
		)
	}
	if err != nil {
		return fmt.Errorf("failed to shard the prow config: %w", err)
	}
	return nil
}
