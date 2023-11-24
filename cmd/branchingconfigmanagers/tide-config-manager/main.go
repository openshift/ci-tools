package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"

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
	prowConfigDir                  string
	shardedProwConfigBaseDir       string
	lifecyclePhase                 string
	excludedReposFile              string
	currentOCPVersion              string
	reposGuardedByAckCriticalFixes string
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
	ackCriticalFixes       = "acknowledge-critical-fixes-only"
	revertCriticalFixes    = "revert-critical-fixes-only"
)

func gatherOptions() (*options, error) {
	o := &options{}
	var errs []error

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	fs.StringVar(&o.shardedProwConfigBaseDir, "sharded-prow-config-base-dir", "", "Basedir for the sharded prow config. If set, org and repo-specific config will get removed from the main prow config and written out in an org/repo tree below the base dir.")
	fs.StringVar(&o.lifecyclePhase, "lifecycle-phase", "", "Lifecycle phase, one of: branching, pre-general-availability, general-availability,acknowledge-critical-fixes-only, revert-critical-fixes-only")
	fs.StringVar(&o.currentOCPVersion, "current-release", "", "Current OCP version")
	fs.StringVar(&o.excludedReposFile, "excluded-repos-config", "", "Path to the GA's excluded repos config file.")
	fs.StringVar(&o.reposGuardedByAckCriticalFixes, "repos-guarded-by-ack-critical-fixes", "", "Path to the list of repos that ack-critical-fixes should be applied to.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		errs = append(errs, fmt.Errorf("couldn't parse arguments: %w", err))
	}

	if o.lifecyclePhase != branching && o.lifecyclePhase != preGeneralAvailability && o.lifecyclePhase != GeneralAvailability && o.lifecyclePhase != ackCriticalFixes && o.lifecyclePhase != revertCriticalFixes {
		errs = append(errs, errors.New("--lifecycle-phase is required and has to be one of: branching, pre-general-availability, general-availability ,acknowledge-critical-fixes-only, revert-critical-fixes-only"))
	}

	if o.lifecyclePhase == branching || o.lifecyclePhase == preGeneralAvailability || o.lifecyclePhase == GeneralAvailability {
		if o.currentOCPVersion == "" {
			errs = append(errs, errors.New("--current-release is required"))
		}
		if _, err := ocplifecycle.ParseMajorMinor(o.currentOCPVersion); o.currentOCPVersion != "" && err != nil {
			errs = append(errs, fmt.Errorf("error parsing current-release %s", o.currentOCPVersion))
		}
	}

	if o.lifecyclePhase == ackCriticalFixes && o.reposGuardedByAckCriticalFixes == "" {
		errs = append(errs, errors.New("--repos-guarded-by-ack-critical-fixes is required when ack-critical-fixes is used"))
	}
	if o.prowConfigDir == "" {
		errs = append(errs, errors.New("--prow-config-dir is required"))
	}
	if o.shardedProwConfigBaseDir == "" {
		errs = append(errs, errors.New("--sharded-prow-config-base-dir is required"))
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
	excludedLabels sets.Set[string]
	mainMaster     sets.Set[string]
	validBug       sets.Set[string]
}

func newSharedDataDelegate() *sharedDataDelegate {
	return &sharedDataDelegate{
		excludedLabels: sets.New[string](qeApproved, docsApproved, pxApproved),
		mainMaster:     sets.New[string](mainBranch, masterBranch),
		validBug:       sets.New[string](validBug),
	}

}

type ackCriticalFixesEvent struct {
	repos sets.Set[string]
	*sharedDataDelegate
}

func newAckCriticalFixesEvent(repos []string, delegate *sharedDataDelegate) ackCriticalFixesEvent {
	return ackCriticalFixesEvent{
		repos:              sets.New[string](repos...),
		sharedDataDelegate: delegate,
	}
}

func (acfe ackCriticalFixesEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(acfe.mainMaster).Len() > 0 && acfe.repos.Has(repo) {
		reqLabels.Insert(ackCriticalFixes)
	}
	q.Labels = sets.List(reqLabels)
}

func (acfe ackCriticalFixesEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {
}

type revertCriticalFixesEvent struct {
	*sharedDataDelegate
}

func newRevertCriticalFixesEvent(delegate *sharedDataDelegate) revertCriticalFixesEvent {
	return revertCriticalFixesEvent{
		sharedDataDelegate: delegate,
	}
}

func (rcfe revertCriticalFixesEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(rcfe.mainMaster).Len() > 0 {
		reqLabels.Delete(ackCriticalFixes)
	}
	q.Labels = sets.List(reqLabels)
}

func (rcfe revertCriticalFixesEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {
}

type branchingDayEvent struct {
	openshiftReleaseBranches sets.Set[string]
	*sharedDataDelegate
}

func newBranchingDayEvent(current string, delegate *sharedDataDelegate) branchingDayEvent {
	return branchingDayEvent{
		openshiftReleaseBranches: sets.New[string](release+current, openshift+current),
		sharedDataDelegate:       delegate,
	}
}

func (bde branchingDayEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(bde.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(staffEngApproved) {
			reqLabels.Delete(staffEngApproved)
			reqLabels.Insert(cherryPickApproved, backportRiskAssessed)
		}
	}
	q.Labels = sets.List(reqLabels)
}

func (bde branchingDayEvent) GetDataFromProwConfig(pc *prowconfig.ProwConfig) {
}

type preGeneralAvailabilityEvent struct {
	openshiftReleaseBranches sets.Set[string]
	*sharedDataDelegate
}

func newPreGeneralAvailability(current string, delegate *sharedDataDelegate) preGeneralAvailabilityEvent {
	return preGeneralAvailabilityEvent{
		openshiftReleaseBranches: sets.New[string](release+current, openshift+current),
		sharedDataDelegate:       delegate,
	}
}

func (pga preGeneralAvailabilityEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(pga.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(cherryPickApproved) && reqLabels.Has(backportRiskAssessed) {
			reqLabels.Insert(staffEngApproved)
		}
	}
	q.Labels = sets.List(reqLabels)
}

func (pga preGeneralAvailabilityEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {
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
	repos                         sets.Set[string]
	excludedAllowList             sets.Set[string]
	noXYAllowList                 sets.Set[string]
	openshiftReleaseBranches      sets.Set[string]
	openshiftReleaseBranchesPlus1 sets.Set[string]
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
	noXYAllowList := sets.New[string](repos.NoXYAllowList...)
	excludedAllowList := sets.New[string](repos.ExcludedAllowList...).Union(noXYAllowList)

	return generalAvailabilityEvent{
		repos:                         sets.New[string](),
		excludedAllowList:             excludedAllowList,
		noXYAllowList:                 noXYAllowList,
		openshiftReleaseBranches:      sets.New[string](release+current, openshift+current),
		openshiftReleaseBranchesPlus1: sets.New[string](release+future, openshift+future),
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
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(gae.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(cherryPickApproved) && reqLabels.Has(backportRiskAssessed) && reqLabels.Has(staffEngApproved) {
			reqLabels.Delete(cherryPickApproved, backportRiskAssessed)
		}
	}
	q.Labels = sets.List(reqLabels)
}

func (gae *generalAvailabilityEvent) ensureCherryPickApprovedLabel(q *prowconfig.TideQuery) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)
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
	q.IncludedBranches = sets.List(branches)
}

func (gae *generalAvailabilityEvent) ensureStaffEngApprovedLabel(q *prowconfig.TideQuery) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if reqLabels.Has(staffEngApproved) {
		if branches.Has(gae.releaseCurrent) {
			branches.Delete(gae.releaseCurrent)
			branches.Insert(gae.releaseFuture)
		}
		if branches.Has(gae.openshiftCurrent) {
			branches.Delete(gae.openshiftCurrent)
			branches.Insert(gae.openshiftFuture)
		}

		if !(branches.Equal(sets.New[string](gae.releaseFuture)) || branches.Equal(sets.New[string](gae.openshiftFuture)) || branches.Equal(gae.openshiftReleaseBranchesPlus1)) {
			fmt.Printf("Suspicious staff-eng-approved query: %s\n", q.Repos)
		}
	}
	q.IncludedBranches = sets.List(branches)
}

func (gae *generalAvailabilityEvent) overrideExcludedBranches(q *prowconfig.TideQuery) {
	branches := sets.New[string](q.ExcludedBranches...)
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
	q.ExcludedBranches = sets.List(branches)
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

	reposGuardedByAckCriticalFixes := []string{}
	if o.reposGuardedByAckCriticalFixes != "" {
		content, err := os.ReadFile(o.reposGuardedByAckCriticalFixes)
		if err != nil {
			return fmt.Errorf("failed to read list of repos to be guarded from file %s: %w", o.reposGuardedByAckCriticalFixes, err)
		}
		reposGuardedByAckCriticalFixes = strings.Split(string(content), "\n")

	}

	return reconcile(o.currentOCPVersion, o.lifecyclePhase, &config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), o.shardedProwConfigBaseDir), repos, reposGuardedByAckCriticalFixes)
}

func reconcile(currentOCPVersion, lifecyclePhase string, config *prowconfig.ProwConfig, target afero.Fs, repos excludedRepos, reposGuardedByAckCriticalFixes []string) error {
	delegate := newSharedDataDelegate()
	var err error
	if lifecyclePhase == ackCriticalFixes {
		_, err = shardprowconfig.ShardProwConfig(config, target, newAckCriticalFixesEvent(reposGuardedByAckCriticalFixes, delegate))
	}

	if lifecyclePhase == revertCriticalFixes {
		_, err = shardprowconfig.ShardProwConfig(config, target, newRevertCriticalFixesEvent(delegate))
	}

	if err != nil {
		return fmt.Errorf("failed to shard the prow config: %w", err)
	}

	if lifecyclePhase == ackCriticalFixes || lifecyclePhase == revertCriticalFixes {
		return nil
	}

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
