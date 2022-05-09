package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

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
	overwriteTimeRaw         string
	overwriteTime            *time.Time
}

func gatherOptions() (*options, error) {
	o := &options{}
	var errs []error

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowConfigDir, "prow-config-dir", "", "Path to the Prow configuration directory.")
	fs.StringVar(&o.shardedProwConfigBaseDir, "sharded-prow-config-base-dir", "", "Basedir for the sharded prow config. If set, org and repo-specific config will get removed from the main prow config and written out in an org/repo tree below the base dir.")
	fs.StringVar(&o.lifecycleConfigFile, "lifecycle-config", "", "Path to the lifecycle config file")
	fs.StringVar(&o.overwriteTimeRaw, "overwrite-time", "", "Act as if this was the current time, must be in RFC3339 format")
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

	if o.overwriteTimeRaw != "" {
		if parsed, err := time.Parse(time.RFC3339, o.overwriteTimeRaw); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse %q as RFC3339 time: %w", o.overwriteTimeRaw, err))
		} else {
			o.overwriteTime = &parsed
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

	return reconcile(event, &config.ProwConfig, afero.NewBasePathFs(afero.NewOsFs(), o.shardedProwConfigBaseDir))
}

func reconcile(event *ocplifecycle.Event, config *prowconfig.ProwConfig, target afero.Fs) error {
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
	if err != nil {
		return fmt.Errorf("failed to shard the prow config: %w", err)
	}
	return nil
}
