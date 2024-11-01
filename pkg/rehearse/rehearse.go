package rehearse

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"
	prowplugins "sigs.k8s.io/prow/pkg/plugins"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
)

const (
	RehearsalsAckLabel             = "rehearsals-ack"
	NetworkAccessRehearsalsOkLabel = "network-access-rehearsals-ok"
	appCIContextName               = string(api.ClusterAPPCI)
)

type RehearsalConfig struct {
	ProwjobKubeconfig string
	KubernetesOptions flagutil.KubernetesOptions

	ProwjobNamespace string
	PodNamespace     string

	NoRegistry bool

	NormalLimit int
	MoreLimit   int
	MaxLimit    int

	StickyLabelAuthors sets.Set[string]

	GCSBucket          string
	GCSCredentialsFile string
	GCSBrowserPrefix   string

	DryRun bool
}

type RehearsalCandidate struct {
	org      string
	repo     string
	base     ref
	head     ref
	prNumber int
	author   string
	title    string
	link     string
}

func RehearsalCandidateFromPullRequest(pullRequest *github.PullRequest, baseSHA string) RehearsalCandidate {
	repo := pullRequest.Base.Repo
	return RehearsalCandidate{
		org:  repo.Owner.Login,
		repo: repo.Name,
		base: ref{
			sha: baseSHA,
			ref: pullRequest.Base.Ref,
		},
		head: ref{
			sha: pullRequest.Head.SHA,
			ref: pullRequest.Head.Ref,
		},
		prNumber: pullRequest.Number,
		author:   pullRequest.User.Login,
		title:    pullRequest.Title,
		link:     pullRequest.HTMLURL,
	}
}

func (rc RehearsalCandidate) createRefs() *prowapi.Refs {
	return &prowapi.Refs{
		Org:     rc.org,
		Repo:    rc.repo,
		BaseRef: rc.base.ref,
		BaseSHA: rc.base.sha,
		Pulls: []prowapi.Pull{
			{
				Number: rc.prNumber,
				Author: rc.author,
				SHA:    rc.head.sha,
				Title:  rc.title,
				Link:   rc.link,
			},
		},
	}
}

type ref struct {
	sha string
	ref string
}

func (r RehearsalConfig) DetermineAffectedJobs(candidate RehearsalCandidate, candidatePath string, networkAccessRehearsalsAllowed bool, logger *logrus.Entry) (config.Presubmits, config.Periodics, []string, error) {
	start := time.Now()
	defer func() {
		logger.Infof("determineAffectedJobs ran in %s", time.Since(start).Truncate(time.Second))
	}()

	prConfig, err := config.GetAllConfigs(candidatePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not load configuration from candidate revision of release repo: %w", err)
	}
	baseSHA := candidate.base.sha
	masterConfig, err := config.GetAllConfigsFromSHA(candidatePath, baseSHA)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not load configuration from base revision of release repo: %w", err)
	}

	presubmits := config.Presubmits{}
	presubmits.AddAll(diffs.GetChangedPresubmits(masterConfig.Prow, prConfig.Prow, logger), config.ChangedPresubmit)
	periodics := config.Periodics{}
	periodics.AddAll(diffs.GetChangedPeriodics(masterConfig.Prow, prConfig.Prow, logger), config.ChangedPeriodic)
	var restrictNetworkAccessFalseJobs []string

	// We can only detect changes if we managed to load both ci-operator config versions
	if masterConfig.CiOperator != nil && prConfig.CiOperator != nil {
		var changedCiopConfigData config.DataByFilename
		var affectedJobs map[string]sets.Set[string]
		changedCiopConfigData, affectedJobs, restrictNetworkAccessFalseJobs = diffs.GetChangedCiopConfigs(masterConfig.CiOperator, prConfig.CiOperator, logger)
		// If we allow network access rehearsals, we can just ignore the returned jobs that set it to 'false'
		if networkAccessRehearsalsAllowed {
			restrictNetworkAccessFalseJobs = []string{}
		}
		presubmitsForCiopConfigs, periodicsForCiopConfigs := diffs.GetJobsForCiopConfigs(prConfig.Prow, changedCiopConfigData, affectedJobs, logger)
		presubmits.AddAll(presubmitsForCiopConfigs, config.ChangedCiopConfig)
		periodics.AddAll(periodicsForCiopConfigs, config.ChangedCiopConfig)
	}

	var changedRegistrySteps []registry.Node
	if !r.NoRegistry {
		changedRegistrySteps, err = determineChangedRegistrySteps(candidatePath, baseSHA, logger)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("could not determine changed registry steps: %w", err)
		}
		presubmitsForRegistry, periodicsForRegistry := SelectJobsForChangedRegistry(changedRegistrySteps, prConfig.Prow.JobConfig.PresubmitsStatic, prConfig.Prow.JobConfig.Periodics, prConfig.CiOperator, logger)
		presubmits.AddAll(presubmitsForRegistry, config.ChangedRegistryContent)
		periodics.AddAll(periodicsForRegistry, config.ChangedRegistryContent)
	}

	return filterPresubmits(presubmits, restrictNetworkAccessFalseJobs, logger), filterPeriodics(periodics, restrictNetworkAccessFalseJobs, logger), restrictNetworkAccessFalseJobs, nil
}

func (r RehearsalConfig) SetupJobs(candidate RehearsalCandidate, candidatePath string, presubmits config.Presubmits, periodics config.Periodics, limit int, logger *logrus.Entry) (*config.ReleaseRepoConfig, *prowapi.Refs, []*prowconfig.Presubmit, error) {
	resolver, err := r.createResolver(candidatePath)
	if err != nil {
		return nil, nil, nil, err
	}
	prConfig, err := config.GetAllConfigs(candidatePath)
	if err != nil {
		return nil, nil, nil, err
	}
	org := candidate.org
	repo := candidate.repo
	prNumber := candidate.prNumber
	prRefs := candidate.createRefs()

	jobConfigurer := NewJobConfigurer(prConfig.CiOperator, prConfig.Prow, resolver, prNumber, logger, prRefs)
	imageStreamTags, presubmitsToRehearse, err := jobConfigurer.ConfigurePresubmitRehearsals(presubmits)
	if err != nil {
		return nil, nil, nil, err
	}

	periodicImageStreamTags, periodicsToRehearse, err := jobConfigurer.ConfigurePeriodicRehearsals(periodics)
	if err != nil {
		return nil, nil, nil, err
	}
	apihelper.MergeImageStreamTagMaps(imageStreamTags, periodicImageStreamTags)

	periodicPresubmits, err := jobConfigurer.ConvertPeriodicsToPresubmits(periodicsToRehearse)
	if err != nil {
		return nil, nil, nil, err
	}
	presubmitsToRehearse = append(presubmitsToRehearse, periodicPresubmits...)

	if rehearsals := len(presubmitsToRehearse); rehearsals == 0 {
		logger.Info("no jobs to rehearse have been found")
		return nil, nil, nil, nil
	} else if rehearsals > limit {
		jobCountFields := logrus.Fields{
			"rehearsal-threshold": limit,
			"rehearsal-jobs":      rehearsals,
		}
		logger.WithFields(jobCountFields).Info("Would rehearse too many jobs, selecting a subset")
		presubmitsToRehearse = determineSubsetToRehearse(presubmitsToRehearse, limit)
	}

	if prConfig.Prow.JobConfig.PresubmitsStatic == nil {
		prConfig.Prow.JobConfig.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
	}
	for _, presubmit := range presubmitsToRehearse {
		prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo] = append(prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo], *presubmit)
	}

	return prConfig, prRefs, presubmitsToRehearse, nil
}

func (r RehearsalConfig) createResolver(candidatePath string) (registry.Resolver, error) {
	var registryRefs registry.ReferenceByName
	var chains registry.ChainByName
	var workflows registry.WorkflowByName
	var observers registry.ObserverByName
	if !r.NoRegistry {
		var err error
		registryRefs, chains, workflows, _, _, _, observers, err = load.Registry(filepath.Join(candidatePath, config.RegistryPath), load.RegistryFlag(0))
		if err != nil {
			return nil, fmt.Errorf("could not load step registry: %w", err)
		}
	}
	resolver := registry.NewResolver(registryRefs, chains, workflows, observers)
	return resolver, nil
}

func (r RehearsalConfig) AbortAllRehearsalJobs(org, repo string, number int, logger *logrus.Entry) {
	_, prowJobConfig := r.getBuildClusterAndProwJobConfigs(logger)
	pjclient, err := NewProwJobClient(prowJobConfig, r.DryRun)
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob client")
	}

	selector := labelSelectorForRehearsalJobs(org, repo, number)
	jobs := &prowapi.ProwJobList{}
	err = pjclient.List(context.TODO(), jobs, selector, ctrlruntimeclient.InNamespace(r.ProwjobNamespace))
	if err != nil {
		logger.WithError(err).Error("failed to list prowjobs for pr")
	}
	logger.Debugf("found %d prowjob(s) to abort", len(jobs.Items))

	for _, job := range jobs.Items {
		// Do not abort jobs that already completed
		if job.Complete() {
			continue
		}
		logger.Debugf("aborting prowjob: %s", job.Name)
		job.Status.State = prowapi.AbortedState
		// We use Update and not Patch here, because we are not the authority of the .Status.State field
		// and must not overwrite changes made to it in the interim by the responsible agent.
		// The accepted trade-off for now is that this leads to failure if unrelated fields where changed
		// by another different actor.
		if err = pjclient.Update(context.TODO(), &job); err != nil && !apierrors.IsConflict(err) {
			logger.WithError(err).Errorf("failed to abort prowjob: %s", job.Name)
		} else {
			logger.Debugf("aborted prowjob: %s", job.Name)
		}
	}
}

func labelSelectorForRehearsalJobs(org, repo string, prNumber int) ctrlruntimeclient.ListOption {
	number := strconv.Itoa(prNumber)
	return ctrlruntimeclient.MatchingLabels{
		kube.OrgLabel:         org,
		kube.RepoLabel:        repo,
		kube.PullLabel:        number,
		kube.ProwJobTypeLabel: string(prowapi.PresubmitJob),
		Label:                 number,
	}
}

// RehearseJobs returns true if the jobs were triggered and succeed
func (r RehearsalConfig) RehearseJobs(
	candidate RehearsalCandidate,
	candidatePath string,
	prRefs *prowapi.Refs,
	presubmitsToRehearse []*prowconfig.Presubmit,
	prowCfg *prowconfig.Config,
	waitForSuccess bool,
	logger *logrus.Entry,
) (bool, error) {
	buildClusterConfigs, prowJobConfig := r.getBuildClusterAndProwJobConfigs(logger)
	pjclient, err := NewProwJobClient(prowJobConfig, r.DryRun)
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob client")
	}

	configUpdaterCfg, err := loadConfigUpdaterCfg(candidatePath)
	if err != nil {
		logger.WithError(err).Fatal("could not load plugin configuration from tested revision of release repo")
	}

	var errs []error
	cleanup, err := setupDependencies(
		presubmitsToRehearse,
		candidate.prNumber,
		buildClusterConfigs,
		logger,
		r.ProwjobNamespace,
		r.PodNamespace,
		configUpdaterCfg,
		candidatePath,
		r.DryRun)
	if err != nil {
		logger.WithError(err).Error("Failed to set up dependencies. This might cause subsequent failures.")
		errs = append(errs, err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	executor := NewExecutor(presubmitsToRehearse, candidate.prNumber, candidatePath, prRefs, r.DryRun, logger, pjclient, r.ProwjobNamespace, prowCfg, waitForSuccess)
	success, err := executor.ExecuteJobs()
	if err != nil {
		logger.WithError(err).Error("Failed to rehearse jobs")
		return false, utilerrors.NewAggregate(errs)
	} else if !success {
		logger.Info("Some jobs failed their rehearsal runs")
	} else if waitForSuccess {
		logger.Info("All jobs were rehearsed successfully")
	} else {
		logger.Info("All jobs were triggered successfully")
	}

	return success, utilerrors.NewAggregate(errs)
}

func (r RehearsalConfig) getBuildClusterAndProwJobConfigs(logger *logrus.Entry) (map[string]rest.Config, *rest.Config) {
	buildClusterConfigs := map[string]rest.Config{}
	var prowJobConfig *rest.Config
	if !r.DryRun {
		var err error
		buildClusterConfigs, err = r.KubernetesOptions.LoadClusterConfigs()
		if err != nil {
			logger.WithError(err).Fatal("failed to read kubeconfigs")
		}
		defaultKubeconfig := buildClusterConfigs[appCIContextName]
		prowJobConfig, err = pjKubeconfig(r.ProwjobKubeconfig, &defaultKubeconfig)
		if err != nil {
			logger.WithError(err).Fatal("Could not load prowjob kubeconfig")
		}
	}

	return buildClusterConfigs, prowJobConfig
}

func determineChangedRegistrySteps(candidate, baseSHA string, logger *logrus.Entry) ([]registry.Node, error) {
	var changedRegistrySteps []registry.Node
	refs, chains, workflows, _, _, _, observers, err := load.Registry(filepath.Join(candidate, config.RegistryPath), load.RegistryFlag(0))
	if err != nil {
		return nil, fmt.Errorf("could not load step registry: %w", err)
	}
	graph, err := registry.NewGraph(refs, chains, workflows, observers)
	if err != nil {
		return nil, fmt.Errorf("could not create step registry graph: %w", err)
	}
	changedRegistrySteps, err = config.GetChangedRegistrySteps(candidate, baseSHA, graph)
	if err != nil {
		return nil, fmt.Errorf("could not get step registry differences: %w", err)
	}
	if len(changedRegistrySteps) != 0 {
		var names []string
		for _, step := range changedRegistrySteps {
			names = append(names, step.Name())
		}
		logger.Infof("Found %d changed registry steps: %s", len(changedRegistrySteps), strings.Join(names, ", "))
	}

	return changedRegistrySteps, nil
}

func loadConfigUpdaterCfg(candidate string) (ret prowplugins.ConfigUpdater, err error) {
	agent := prowplugins.ConfigAgent{}
	if err = agent.Load(filepath.Join(candidate, config.PluginConfigInRepoPath), []string{filepath.Join(candidate, filepath.Dir(config.PluginConfigInRepoPath))}, "_pluginconfig.yaml", true, false); err == nil {
		ret = agent.Config().ConfigUpdater
	}
	return
}

// determineSubsetToRehearse determines in a sophisticated way which subset of jobs should be chosen to be rehearsed.
// First, it will create a list of the jobs mapped by the source type and calculates the maximum allowed jobs for each
// source type. If there are jobs from a specific source type that are under the max allowed number, it will fill the gap
// from the other not chosen jobs until it reaches the rehearsal limit.
func determineSubsetToRehearse(presubmitsToRehearse []*prowconfig.Presubmit, rehearsalLimit int) []*prowconfig.Presubmit {
	if len(presubmitsToRehearse) <= rehearsalLimit {
		return presubmitsToRehearse
	}

	presubmitsBySourceType := make(map[config.SourceType][]*prowconfig.Presubmit)
	for _, p := range presubmitsToRehearse {
		sourceType := config.GetSourceType(p.Labels)
		presubmitsBySourceType[sourceType] = append(presubmitsBySourceType[sourceType], p)
	}

	maxJobsPerSourceType := rehearsalLimit / len(presubmitsBySourceType)
	var toRehearse []*prowconfig.Presubmit
	var dropped []*prowconfig.Presubmit

	for _, jobs := range presubmitsBySourceType {
		if len(jobs) > maxJobsPerSourceType {
			dropped = append(dropped, jobs[maxJobsPerSourceType:]...)
			jobs = jobs[:maxJobsPerSourceType]
		}
		toRehearse = append(toRehearse, jobs...)
	}

	// There are two ways that we will hit this check. First, jobs from a specific resource are less than the
	// maximum allowed, we will end up having less rehearsals than the limit. Second, summary of the maximum allowed jobs
	// from each source can be lower than the rehearse limit  due to the rounding inherent in integer division.
	// In both cases, we fill up the gap from the jobs that we didn't pick earlier.
	if len(toRehearse) <= rehearsalLimit {
		sort.Slice(dropped, func(a, b int) bool { return dropped[a].Name < dropped[b].Name })
		toRehearse = append(toRehearse, dropped[:rehearsalLimit-len(toRehearse)]...)
	}

	return toRehearse
}

type cleanup func()
type cleanups []cleanup

func (c cleanups) cleanup() {
	for _, cleanup := range c {
		cleanup()
	}
}

func setupDependencies(
	jobs []*prowconfig.Presubmit,
	prNumber int,
	configs map[string]rest.Config,
	log *logrus.Entry,
	prowJobNamespace string,
	podNamespace string,
	configUpdaterCfg prowplugins.ConfigUpdater,
	releaseRepoPath string,
	dryRun bool,
) (cleanup, error) {
	buildClusters := sets.Set[string]{}
	for _, job := range jobs {
		if _, ok := configs[job.Cluster]; !ok && !dryRun {
			return nil, fmt.Errorf("no config for buildcluster %s provided", job.Cluster)
		}
		buildClusters.Insert(job.Cluster)
	}

	var cleanups cleanups
	cleanupsLock := &sync.Mutex{}

	// Otherwise we flake in integration tests because we just capture stdout. Its not
	// really possible to sort this as we use a client per cluster. Furthermore the output
	// doesn't even contain the info which cluster was used.
	// TODO: Remove the whole dry-run concept and write tests that just pass in a fakeclient.
	if dryRun {
		if len(buildClusters) > 1 {
			buildClusters = sets.New[string]("default")
		}
	}

	g, _ := errgroup.WithContext(context.Background())
	for _, cluster := range buildClusters.UnsortedList() {
		buildCluster := cluster
		g.Go(func() error {
			log := log.WithField("buildCluster", buildCluster)
			clusterConfig := configs[buildCluster]
			cmClient, err := NewCMClient(&clusterConfig, podNamespace, dryRun)
			if err != nil {
				log.WithError(err).Error("could not create a configMap client")
			}

			cmManager := NewCMManager(buildCluster, prowJobNamespace, cmClient, configUpdaterCfg, prNumber, releaseRepoPath, log)

			cleanupsLock.Lock()
			cleanups = append(cleanups, func() {
				if err := cmManager.Clean(); err != nil {
					log.WithError(err).Error("failed to clean up temporary ConfigMaps")
				}
			})
			cleanupsLock.Unlock()

			return nil
		})
	}

	return cleanups.cleanup, g.Wait()
}

func pjKubeconfig(path string, defaultKubeconfig *rest.Config) (*rest.Config, error) {
	if path == "" {
		return defaultKubeconfig, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}

// FilterJobsByRequested returns only those presubmits and periodics that appear in the requested slice. It also returns a slice of all jobs not found in the original sets.
func FilterJobsByRequested(requested []string, presubmits config.Presubmits, periodics config.Periodics, logger *logrus.Entry) (config.Presubmits, config.Periodics, []string) {
	filteredPresubmits, filteredPeriodics := config.Presubmits{}, config.Periodics{}
	var unaffected []string
	for _, requestedJob := range requested {
		numChecked := 0
		logger = logger.WithField("requestedJob", requestedJob)
		logger.Debug("requested to run")

		found := false
		for repo, jobs := range presubmits {
			for _, job := range jobs {
				numChecked++
				logger.Tracef("checking against: %s", job.Name)
				if job.Name == requestedJob {
					found = true
					logger.Debug("presubmit was found to be affected")
					filteredPresubmits.Add(repo, job, config.ChangedPresubmit)
					break
				}
			}
			if found {
				break
			}
		}

		if !found {
			for _, job := range periodics {
				numChecked++
				logger.Tracef("checking against: %s", job.Name)
				if job.Name == requestedJob {
					found = true
					logger.Debug("periodic was found to be affected")
					filteredPeriodics.Add(job, config.ChangedPeriodic)
					break
				}
			}
		}

		if !found {
			logger.Debug("job wasn't found to be affected")
			unaffected = append(unaffected, requestedJob)
		}

		logger.Debugf("total number of jobs checked: %d", numChecked)
	}

	return filteredPresubmits, filteredPeriodics, unaffected
}
