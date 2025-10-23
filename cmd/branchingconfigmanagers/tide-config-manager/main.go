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
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
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
	verifiedOptInFile              string
	verifiedOptOutFile             string
	ciOperatorConfigDir            string
}

const (
	branching              = "branching"
	preGeneralAvailability = "pre-general-availability"
	GeneralAvailability    = "general-availability"
	verified               = "verified"
	staffEngApproved       = "staff-eng-approved"
	backportRiskAssessed   = "backport-risk-assessed"
	qeApproved             = "qe-approved"
	docsApproved           = "docs-approved"
	pxApproved             = "px-approved"
	validBug               = "jira/valid-bug"
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
	fs.StringVar(&o.lifecyclePhase, "lifecycle-phase", "", "Lifecycle phase, one of: branching, pre-general-availability, general-availability,acknowledge-critical-fixes-only, revert-critical-fixes-only, verified")
	fs.StringVar(&o.currentOCPVersion, "current-release", "", "Current OCP version")
	fs.StringVar(&o.excludedReposFile, "excluded-repos-config", "", "Path to the GA's excluded repos config file.")
	fs.StringVar(&o.reposGuardedByAckCriticalFixes, "repos-guarded-by-ack-critical-fixes", "", "Path to the list of repos that ack-critical-fixes should be applied to.")
	fs.StringVar(&o.verifiedOptInFile, "verified-opt-in", "", "Path to the verified opt-in file.")
	fs.StringVar(&o.verifiedOptOutFile, "verified-opt-out", "", "Path to the verified opt-out file.")
	fs.StringVar(&o.ciOperatorConfigDir, "ci-operator-config-dir", "", "Path to the ci-operator config directory.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		errs = append(errs, fmt.Errorf("couldn't parse arguments: %w", err))
	}

	if o.lifecyclePhase != branching && o.lifecyclePhase != preGeneralAvailability && o.lifecyclePhase != GeneralAvailability && o.lifecyclePhase != ackCriticalFixes && o.lifecyclePhase != revertCriticalFixes && o.lifecyclePhase != verified {
		errs = append(errs, errors.New("--lifecycle-phase is required and has to be one of: branching, pre-general-availability, general-availability ,acknowledge-critical-fixes-only, revert-critical-fixes-only, verified"))
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

	if o.lifecyclePhase == verified {
		if o.verifiedOptInFile == "" {
			errs = append(errs, errors.New("--verified-opt-in is required when verified lifecycle phase is used"))
		}
		if o.verifiedOptOutFile == "" {
			errs = append(errs, errors.New("--verified-opt-out is required when verified lifecycle phase is used"))
		}
		if o.ciOperatorConfigDir == "" {
			errs = append(errs, errors.New("--ci-operator-config-dir is required when verified lifecycle phase is used"))
		}
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

	if branches.Len() >= 1 && branches.Len() <= 2 {
		if branches.Intersection(bde.openshiftReleaseBranches).Len() > 0 {
			if reqLabels.Has(staffEngApproved) {
				reqLabels.Delete(staffEngApproved)
				reqLabels.Insert(backportRiskAssessed)
			}
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
		if reqLabels.Has(backportRiskAssessed) {
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
	gae.deleteBackportRiskAssesedLabels(q)
	gae.ensureStaffEngApprovedLabel(q)
	gae.overrideExcludedBranches(q)
}

func (gae generalAvailabilityEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {}

func (gae *generalAvailabilityEvent) deleteBackportRiskAssesedLabels(q *prowconfig.TideQuery) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	if branches.Intersection(gae.openshiftReleaseBranches).Len() > 0 {
		if reqLabels.Has(backportRiskAssessed) && reqLabels.Has(staffEngApproved) {
			reqLabels.Delete(backportRiskAssessed)
		}
	}
	q.Labels = sets.List(reqLabels)
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

		if !branches.Equal(sets.New[string](gae.releaseFuture)) && !branches.Equal(sets.New[string](gae.openshiftFuture)) && !branches.Equal(gae.openshiftReleaseBranchesPlus1) {
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

type verifiedEvent struct {
	optInRepos  sets.Set[string]
	optOutRepos sets.Set[string]
	*sharedDataDelegate
}

func newVerifiedEvent(optInFile, optOutFile, ciOperatorConfigDir string, delegate *sharedDataDelegate) (*verifiedEvent, error) {
	return newVerifiedEventWithDeps(
		optInFile,
		optOutFile,
		ciOperatorConfigDir,
		delegate,
		readRepoListYAML,
		config.OperateOnCIOperatorConfigDir,
	)
}

// newVerifiedEventWithDeps is the testable version that accepts dependencies
func newVerifiedEventWithDeps(
	optInFile, optOutFile, ciOperatorConfigDir string,
	delegate *sharedDataDelegate,
	fileReader func(string) ([]string, error),
	configDirOperator func(string, config.ConfigIterFunc) error,
) (*verifiedEvent, error) {
	optInRepos := sets.New[string]()
	optOutRepos := sets.New[string]()

	// Load opt-in repos from YAML file
	if optInFile != "" {
		repos, err := fileReader(optInFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read verified opt-in file: %w", err)
		}
		optInRepos = sets.New[string](repos...)
	}

	// Load opt-out repos from YAML file
	if optOutFile != "" {
		repos, err := fileReader(optOutFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read verified opt-out file: %w", err)
		}
		optOutRepos = sets.New[string](repos...)
	}

	// Add repos with OCP promotion from ci-operator configs
	ocpPromotionRepos := sets.New[string]()
	err := configDirOperator(ciOperatorConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		if configuration.PromotionConfiguration != nil {
			for _, target := range configuration.PromotionConfiguration.Targets {
				if target.Namespace == "ocp" {
					repo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
					ocpPromotionRepos.Insert(repo)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to process ci-operator configs: %w", err)
	}

	finalOptInRepos := optInRepos.Union(ocpPromotionRepos).Difference(optOutRepos)

	return &verifiedEvent{
		optInRepos:         finalOptInRepos,
		optOutRepos:        optOutRepos,
		sharedDataDelegate: delegate,
	}, nil
}

// readRepoListYAML reads a YAML file containing org: [list of repos] format
func readRepoListYAML(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	trimmedData := strings.TrimSpace(string(data))
	if trimmedData == "" {
		return []string{}, nil
	}

	var orgRepoMap map[string][]string
	if err := yaml.Unmarshal(data, &orgRepoMap); err != nil {
		return nil, fmt.Errorf("failed to parse YAML file %s: %w", filePath, err)
	}

	var repos []string
	for org, repoList := range orgRepoMap {
		for _, repo := range repoList {
			repos = append(repos, fmt.Sprintf("%s/%s", org, repo))
		}
	}

	return repos, nil
}

func (ve *verifiedEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	reqLabels := sets.New[string](q.Labels...)
	branches := sets.New[string](q.IncludedBranches...)

	// Only apply verified label to repos that are opt-in and not opt-out
	if ve.optInRepos.Has(repo) && !ve.optOutRepos.Has(repo) {
		shouldAddVerified := false

		if branches.Intersection(ve.mainMaster).Len() > 0 {
			shouldAddVerified = true
		}

		for branch := range branches {
			if isVersionedBranch(branch) {
				shouldAddVerified = true
				break
			}
		}

		if shouldAddVerified {
			reqLabels.Insert("verified")
		}
	}
	q.Labels = sets.List(reqLabels)
}

// isVersionedBranch checks if a branch name matches release-4.x or openshift-4.x pattern
func isVersionedBranch(branch string) bool {
	if strings.HasPrefix(branch, "release-4.") {
		versionPart := strings.TrimPrefix(branch, "release-4.")
		if isValidMinorVersion(versionPart) {
			return true
		}
	}

	if strings.HasPrefix(branch, "openshift-4.") {
		versionPart := strings.TrimPrefix(branch, "openshift-4.")
		if isValidMinorVersion(versionPart) {
			return true
		}
	}

	return false
}

// isValidMinorVersion checks if a string represents a valid minor version (e.g., "9", "10", "15")
func isValidMinorVersion(version string) bool {
	if version == "" {
		return false
	}

	for _, char := range version {
		if char < '0' || char > '9' {
			return false
		}
	}

	return version != "0"
}

func (ve *verifiedEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {
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

	return reconcile(o.currentOCPVersion, o.lifecyclePhase, &config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), o.shardedProwConfigBaseDir), repos, reposGuardedByAckCriticalFixes, o.verifiedOptInFile, o.verifiedOptOutFile, o.ciOperatorConfigDir)
}

func reconcile(currentOCPVersion, lifecyclePhase string, config *prowconfig.ProwConfig, target afero.Fs, repos excludedRepos, reposGuardedByAckCriticalFixes []string, verifiedOptInFile, verifiedOptOutFile, ciOperatorConfigDir string) error {
	delegate := newSharedDataDelegate()
	var err error
	if lifecyclePhase == ackCriticalFixes {
		_, err = shardprowconfig.ShardProwConfig(config, target, newAckCriticalFixesEvent(reposGuardedByAckCriticalFixes, delegate))
	}

	if lifecyclePhase == revertCriticalFixes {
		_, err = shardprowconfig.ShardProwConfig(config, target, newRevertCriticalFixesEvent(delegate))
	}

	if lifecyclePhase == verified {
		verifiedEvt, err := newVerifiedEvent(verifiedOptInFile, verifiedOptOutFile, ciOperatorConfigDir, delegate)
		if err != nil {
			return fmt.Errorf("failed to create verified event: %w", err)
		}
		_, err = shardprowconfig.ShardProwConfig(config, target, verifiedEvt)
		if err != nil {
			return fmt.Errorf("failed to shard the prow config: %w", err)
		}
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to shard the prow config: %w", err)
	}

	if lifecyclePhase == ackCriticalFixes || lifecyclePhase == revertCriticalFixes || lifecyclePhase == verified {
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
