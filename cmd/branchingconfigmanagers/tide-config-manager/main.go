package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

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
	lifecycleConfigFile      string
	excludedReposFile        string
	overrideTimeRaw          string
	overrideTime             *time.Time
}

func gatherOptions() (*options, error) {
	o := &options{}
	var errs []error

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	fs.StringVar(&o.shardedProwConfigBaseDir, "sharded-prow-config-base-dir", "", "Basedir for the sharded prow config. If set, org and repo-specific config will get removed from the main prow config and written out in an org/repo tree below the base dir.")
	fs.StringVar(&o.lifecycleConfigFile, "lifecycle-config", "", "Path to the lifecycle config file")
	fs.StringVar(&o.excludedReposFile, "excluded-repos-config", "", "Path to the GA's excluded repos config file.")
	fs.StringVar(&o.overrideTimeRaw, "override-time", "", "Act as if this was the current time, must be in RFC3339 format")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		errs = append(errs, fmt.Errorf("couldn't parse arguments: %w", err))
	}

	if o.lifecycleConfigFile == "" {
		errs = append(errs, errors.New("--lifecycle-config is required"))
	}
	if o.prowConfigDir == "" {
		errs = append(errs, errors.New("--prow-config-dir is required"))
	}
	if o.shardedProwConfigBaseDir == "" {
		errs = append(errs, errors.New("--sharded-prow-config-base-dir is required"))
	}

	if o.overrideTimeRaw != "" {
		if parsed, err := time.Parse(time.RFC3339, o.overrideTimeRaw); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse %q as RFC3339 time: %w", o.overrideTimeRaw, err))
		} else {
			o.overrideTime = &parsed
		}
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	if err := updateProwConfigs(o, time.Now()); err != nil {
		logrus.WithError(err).Fatal("could not update Prow configuration")
	}
}

const (
	staffEngApproved   = "staff-eng-approved"
	cherryPickApproved = "cherry-pick-approved"
	qeApproved         = "qe-approved"
	docsApproved       = "docs-approved"
	pxApproved         = "px-approved"
	validBug           = "bugzilla/valid-bug"
	release            = "release-"
	openshift          = "openshift-"
	mainBranch         = "main"
	masterBranch       = "master"
	openshiftPriv      = "openshift-priv"
	ocpProductName     = "ocp"
)

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

type featureFreezeEvent struct {
	excludedOrgs                  []string
	repos                         sets.String
	openshiftReleaseBranches      sets.String
	openshiftReleaseBranchesPlus1 sets.String
	*sharedDataDelegate
}

func newFeatureFreezeEvent(current, future string, delegate *sharedDataDelegate) featureFreezeEvent {
	return featureFreezeEvent{
		excludedOrgs:                  []string{openshiftPriv},
		repos:                         sets.NewString(),
		openshiftReleaseBranches:      sets.NewString(release+current, openshift+current),
		openshiftReleaseBranchesPlus1: sets.NewString(release+future, openshift+future),
		sharedDataDelegate:            delegate,
	}
}

func (ffe featureFreezeEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	ffe.ensureFeatureFreezeApprovals(q)
	if ffe.repos.Has(repo) {
		ffe.ensureFeatureFreezeBugs(q)
	}
}

func (ffe featureFreezeEvent) GetDataFromProwConfig(pc *prowconfig.ProwConfig) {
	for _, query := range pc.Tide.Queries {
		branches := sets.NewString(query.IncludedBranches...)
		labels := sets.NewString(query.Labels...)
		ffe.rewriteReposToExcludeOrgs(&query)
		if branches.Intersection(ffe.openshiftReleaseBranchesPlus1).Len() > 0 && labels.Has(staffEngApproved) {
			ffe.repos.Insert(query.Repos...)
			continue
		}
		if branches.Intersection(ffe.openshiftReleaseBranches).Len() > 0 && labels.Has(cherryPickApproved) {
			ffe.repos.Insert(query.Repos...)
		}
	}
}

func (ffe *featureFreezeEvent) rewriteReposToExcludeOrgs(q *prowconfig.TideQuery) {
	repos := []string{}
	for _, repo := range q.Repos {
		for _, org := range ffe.excludedOrgs {
			if strings.Contains(repo, org) {
				continue
			}
			repos = append(repos, repo)
		}
	}
	q.Repos = repos
}

func (ffe *featureFreezeEvent) ensureFeatureFreezeBugs(q *prowconfig.TideQuery) {
	requiredLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)
	if branches.Intersection(ffe.mainMaster).Len() == 0 {
		return
	}
	if requiredLabels.Intersection(ffe.excludedLabels).Len() > 0 {
		return
	}
	requiredLabels = requiredLabels.Union(ffe.validBug)
	q.Labels = requiredLabels.List()
}

func (ffe *featureFreezeEvent) ensureFeatureFreezeApprovals(q *prowconfig.TideQuery) {
	requiredLabels := sets.NewString(q.Labels...)
	branches := sets.NewString(q.IncludedBranches...)
	if branches.Intersection(ffe.mainMaster).Len() == 0 {
		return
	}
	if requiredLabels.Intersection(ffe.excludedLabels).Len() == 0 {
		return
	}
	requiredLabels = requiredLabels.Union(ffe.excludedLabels)
	requiredLabels = requiredLabels.Difference(ffe.validBug)
	q.Labels = requiredLabels.List()
}

type codeFreezeEvent struct {
	repos                          sets.String
	noFeatureFreezeRepos           sets.String
	bugzillaLabelOnMainMasterRepos sets.String
	*sharedDataDelegate
}

func newCodeFreezeEvent(delegate *sharedDataDelegate) codeFreezeEvent {
	return codeFreezeEvent{
		repos:                          sets.NewString(),
		noFeatureFreezeRepos:           sets.NewString(),
		bugzillaLabelOnMainMasterRepos: sets.NewString(),
		sharedDataDelegate:             delegate,
	}
}

func (cfe codeFreezeEvent) ModifyQuery(q *prowconfig.TideQuery, repo string) {
	if cfe.repos.Has(repo) {
		branches := sets.NewString(q.IncludedBranches...)
		if len(branches.Intersection(cfe.mainMaster)) == 0 {
			return
		}
		q.Labels = sets.NewString(q.Labels...).Difference(cfe.validBug).List()
	}
}

func (cfe codeFreezeEvent) GetDataFromProwConfig(pc *prowconfig.ProwConfig) {
	for _, query := range pc.Tide.Queries {
		branches := sets.NewString(query.IncludedBranches...)
		if len(branches.Intersection(cfe.mainMaster)) == 0 {
			continue
		}
		labels := sets.NewString(query.Labels...)
		if len(labels.Intersection(cfe.excludedLabels)) > 0 {
			for _, repo := range query.Repos {
				cfe.noFeatureFreezeRepos.Insert(repo)
			}
		}
		if len(labels.Intersection(cfe.validBug)) > 0 {
			for _, repo := range query.Repos {
				cfe.bugzillaLabelOnMainMasterRepos.Insert(repo)
			}
		}
	}
	for repo := range cfe.bugzillaLabelOnMainMasterRepos.Difference(cfe.noFeatureFreezeRepos) {
		cfe.repos.Insert(repo)
	}
}

type excludedRepos struct {
	NoXYAllowList     []string `yaml:"NoXYAllowList,flow"`
	ExcludedAllowList []string `yaml:"ExcludedAllowList,flow"`
}

func (er *excludedRepos) loadExcludedReposConfig(path string) error {
	cfgBytes, err := ioutil.ReadFile(path)
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
	gae.ensureStaffEngApprovedLabel(q)
	gae.ensureCherryPickApprovedLabel(q)
	gae.overrideExcludedBranches(q)
}

func (gae generalAvailabilityEvent) GetDataFromProwConfig(*prowconfig.ProwConfig) {}

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

func updateProwConfigs(o *options, now time.Time) error {
	configPath := path.Join(o.prowConfigDir, config.ProwConfigFile)
	var additionalConfigs []string
	additionalConfigs = append(additionalConfigs, o.shardedProwConfigBaseDir)

	config, err := prowconfig.LoadStrict(configPath, "", additionalConfigs, "_prowconfig.yaml")
	if err != nil {
		return fmt.Errorf("failed to load Prow config in strict mode: %w", err)
	}

	lifecycleConfig, err := ocplifecycle.LoadConfig(o.lifecycleConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load the lifecycle configuration: %w", err)
	}

	timelineOpts := ocplifecycle.TimelineOptions{
		OnlyEvents: sets.NewString([]string{
			string(ocplifecycle.LifecycleEventFeatureFreeze),
			string(ocplifecycle.LifecycleEventCodeFreeze),
			string(ocplifecycle.LifecycleEventGenerallyAvailable),
		}...),
	}

	timeline := lifecycleConfig.GetTimeline(ocpProductName, timelineOpts)
	event := timeline.GetExactLifecyclePhase(now)
	if event == nil {
		return nil
	}

	repos := excludedRepos{}
	if o.excludedReposFile != "" {
		if err = repos.loadExcludedReposConfig(o.excludedReposFile); err != nil {
			return fmt.Errorf("failed to load the excluded repos configuration: %w", err)
		}
	}
	return reconcile(event, &config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), o.shardedProwConfigBaseDir), repos)
}

func reconcile(event *ocplifecycle.Event, config *prowconfig.ProwConfig, target afero.Fs, repos excludedRepos) error {
	delegate := newSharedDataDelegate()
	currentVersion, err := ocplifecycle.ParseMajorMinor(event.ProductVersion)
	if err != nil {
		return fmt.Errorf("failed to parse %s as majorMinor version: %w", event.ProductVersion, err)
	}
	if event.LifecyclePhase.Event == ocplifecycle.LifecycleEventFeatureFreeze {
		_, err = shardprowconfig.ShardProwConfig(config, target,
			newFeatureFreezeEvent(
				currentVersion.GetVersion(),
				currentVersion.GetFutureVersion(),
				delegate),
		)
	}
	if event.LifecyclePhase.Event == ocplifecycle.LifecycleEventCodeFreeze {
		_, err = shardprowconfig.ShardProwConfig(config, target, newCodeFreezeEvent(delegate))
	}
	if event.LifecyclePhase.Event == ocplifecycle.LifecycleEventGenerallyAvailable {
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
