package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	prowplugins "k8s.io/test-infra/prow/plugins"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/rehearse"
)

const (
	appCIContextName = string(api.ClusterAPPCI)
)

type rehearsalConfig struct {
	prowjobKubeconfig string
	kubernetesOptions flagutil.KubernetesOptions

	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	normalLimit int
	moreLimit   int
	maxLimit    int

	dryRun bool
}

func rehearsalConfigFromOptions(o options) rehearsalConfig {
	return rehearsalConfig{
		prowjobKubeconfig: o.prowjobKubeconfig,
		kubernetesOptions: o.kubernetesOptions,
		noTemplates:       o.noTemplates,
		noRegistry:        o.noRegistry,
		noClusterProfiles: o.noClusterProfiles,
		normalLimit:       o.normalLimit,
		moreLimit:         o.moreLimit,
		maxLimit:          o.maxLimit,
		dryRun:            o.dryRun,
	}
}

func (r rehearsalConfig) determineAffectedJobs(pullRequest github.PullRequest, candidate string, logger *logrus.Entry) (config.Presubmits, config.Periodics, rehearse.ConfigMaps, rehearse.ConfigMaps) {
	start := time.Now()
	defer func() {
		logger.Infof("determinedAffectedJobs ran in %s", time.Since(start).Truncate(time.Second))
	}()

	prConfig := config.GetAllConfigs(candidate, logger)
	baseSHA := pullRequest.Base.SHA
	masterConfig, err := config.GetAllConfigsFromSHA(candidate, baseSHA, logger)
	if err != nil {
		logger.WithError(err).Fatal("could not load configuration from base revision of release repo")
	}

	// We always need both Prow config versions, otherwise we cannot compare them
	if masterConfig.Prow == nil || prConfig.Prow == nil {
		logger.WithError(err).Fatal("could not load Prow configs from base or tested revision of release repo")
	}
	// We always need PR versions of ciop config, otherwise we cannot provide them to rehearsed jobs
	if prConfig.CiOperator == nil {
		logger.WithError(err).Fatal("could not load ci-operator configs from tested revision of release repo")
	}

	configUpdaterCfg, err := loadConfigUpdaterCfg(candidate)
	if err != nil {
		logger.WithError(err).Fatal("could not load plugin configuration from tested revision of release repo")
	}

	presubmits := config.Presubmits{}
	periodics := config.Periodics{}

	changedPeriodics := diffs.GetChangedPeriodics(masterConfig.Prow, prConfig.Prow, logger)
	periodics.AddAll(changedPeriodics, config.ChangedPeriodic)
	changedPresubmits := diffs.GetChangedPresubmits(masterConfig.Prow, prConfig.Prow, logger)
	presubmits.AddAll(changedPresubmits, config.ChangedPresubmit)

	// We can only detect changes if we managed to load both ci-operator config versions
	if masterConfig.CiOperator != nil && prConfig.CiOperator != nil {
		changedCiopConfigData, affectedJobs := diffs.GetChangedCiopConfigs(masterConfig.CiOperator, prConfig.CiOperator, logger)
		presubmitsForCiopConfigs, periodicsForCiopConfigs := diffs.GetJobsForCiopConfigs(prConfig.Prow, changedCiopConfigData, affectedJobs, logger)
		presubmits.AddAll(presubmitsForCiopConfigs, config.ChangedCiopConfig)
		periodics.AddAll(periodicsForCiopConfigs, config.ChangedCiopConfig)
	}

	loggers := rehearse.Loggers{Job: logger, Debug: logger} //TODO: same logger for both. Once the original pj-rehearse is gone we can clean this up and just pass a logger
	var changedRegistrySteps []registry.Node
	if !r.noRegistry {
		changedRegistrySteps = determineChangedRegistrySteps(candidate, baseSHA, logger)
		presubmitsForRegistry, periodicsForRegistry := rehearse.SelectJobsForChangedRegistry(changedRegistrySteps, prConfig.Prow.JobConfig.PresubmitsStatic, prConfig.Prow.JobConfig.Periodics, prConfig.CiOperator, loggers)
		presubmits.AddAll(presubmitsForRegistry, config.ChangedRegistryContent)
		periodics.AddAll(periodicsForRegistry, config.ChangedRegistryContent)
	}

	var changedTemplates rehearse.ConfigMaps
	if !r.noTemplates {
		changedTemplates = determineChangedTemplates(candidate, baseSHA, pullRequest.Head.SHA, pullRequest.Number, configUpdaterCfg, logger)
		randomJobsForChangedTemplates := rehearse.AddRandomJobsForChangedTemplates(changedTemplates.ProductionNames, presubmits, prConfig.Prow.JobConfig.PresubmitsStatic, loggers)
		presubmits.AddAll(randomJobsForChangedTemplates, config.ChangedTemplate)
	}

	var changedClusterProfiles rehearse.ConfigMaps
	if !r.noClusterProfiles {
		changedClusterProfiles = determineChangedClusterProfiles(candidate, baseSHA, pullRequest.Head.SHA, pullRequest.Number, configUpdaterCfg, logger)
		presubmitsForClusterProfiles := diffs.GetPresubmitsForClusterProfiles(prConfig.Prow, changedClusterProfiles.ProductionNames, logger)
		presubmits.AddAll(presubmitsForClusterProfiles, config.ChangedClusterProfile)
	}

	return presubmits, periodics, changedTemplates, changedClusterProfiles
}

func (r rehearsalConfig) rehearseJobs(pullRequest github.PullRequest, candidate string, presubmits config.Presubmits, periodics config.Periodics, rehearsalTemplates, rehearsalClusterProfiles rehearse.ConfigMaps, limit int, logger *logrus.Entry) error {
	start := time.Now()
	defer func() {
		logger.Infof("rehearseJobs ran in %s", time.Since(start).Truncate(time.Second))
	}()

	var registryRefs registry.ReferenceByName
	var chains registry.ChainByName
	var workflows registry.WorkflowByName
	var observers registry.ObserverByName
	if !r.noRegistry {
		var err error
		registryRefs, chains, workflows, _, _, observers, err = load.Registry(filepath.Join(candidate, config.RegistryPath), load.RegistryFlag(0))
		if err != nil {
			logger.WithError(err).Fatal("could not load step registry")
		}
	}
	resolver := registry.NewResolver(registryRefs, chains, workflows, observers)
	prConfig := config.GetAllConfigs(candidate, logger)
	org := pullRequest.Base.Repo.Owner.Login
	repo := pullRequest.Base.Repo.Name
	prNumber := pullRequest.Number
	prRefs := &pjapi.Refs{
		Org:     org,
		Repo:    repo,
		BaseRef: pullRequest.Base.Ref,
		BaseSHA: pullRequest.Base.SHA,
		Pulls: []prowapi.Pull{
			{
				Number:     prNumber,
				Author:     pullRequest.User.Login,
				SHA:        pullRequest.Head.SHA,
				Title:      pullRequest.Title,
				Ref:        pullRequest.Head.Ref,
				Link:       pullRequest.HTMLURL,
				AuthorLink: pullRequest.User.HTMLURL,
			},
		},
	}

	loggers := rehearse.Loggers{Job: logger, Debug: logger} //TODO: same logger for both. Once the original pj-rehearse is gone we can clean this up and just pass a logger
	jobConfigurer := rehearse.NewJobConfigurer(prConfig.CiOperator, prConfig.Prow, resolver, prNumber, loggers, rehearsalTemplates.Names, rehearsalClusterProfiles.Names, prRefs)
	imagestreamtags, presubmitsToRehearse, err := jobConfigurer.ConfigurePresubmitRehearsals(presubmits)
	if err != nil {
		return err
	}

	periodicImageStreamTags, periodicsToRehearse, err := jobConfigurer.ConfigurePeriodicRehearsals(periodics)
	if err != nil {
		return err
	}
	apihelper.MergeImageStreamTagMaps(imagestreamtags, periodicImageStreamTags)

	periodicPresubmits, err := jobConfigurer.ConvertPeriodicsToPresubmits(periodicsToRehearse)
	if err != nil {
		return err
	}
	presubmitsToRehearse = append(presubmitsToRehearse, periodicPresubmits...)

	if rehearsals := len(presubmitsToRehearse); rehearsals == 0 {
		return errors.New("no jobs found to rehearse")
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

	if err := prConfig.Prow.ValidateJobConfig(); err != nil {
		logger.WithError(err).Fatal("jobconfig validation failed")
	}

	buildClusterConfigs := map[string]rest.Config{}
	var prowJobConfig *rest.Config
	if !r.dryRun {
		buildClusterConfigs, err = r.kubernetesOptions.LoadClusterConfigs()
		if err != nil {
			logger.WithError(err).Fatal("failed to read kubeconfigs")
		}
		defaultKubeconfig := buildClusterConfigs[appCIContextName]
		prowJobConfig, err = pjKubeconfig(r.prowjobKubeconfig, &defaultKubeconfig)
		if err != nil {
			logger.WithError(err).Fatal("Could not load prowjob kubeconfig")
		}
	}

	pjclient, err := rehearse.NewProwJobClient(prowJobConfig, r.dryRun)
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob client")
	}

	configUpdaterCfg, err := loadConfigUpdaterCfg(candidate)
	if err != nil {
		logger.WithError(err).Fatal("could not load plugin configuration from tested revision of release repo")
	}

	var errs []error
	cleanup, err := setupDependencies(
		presubmitsToRehearse,
		prNumber,
		buildClusterConfigs,
		logger,
		prConfig.Prow.ProwJobNamespace,
		pjclient,
		prConfig.Prow.PodNamespace,
		configUpdaterCfg,
		candidate,
		r.dryRun,
		rehearsalTemplates,
		rehearsalClusterProfiles,
		imagestreamtags)
	if err != nil {
		logger.WithError(err).Error("Failed to set up dependencies. This might cause subsequent failures.")
		errs = append(errs, err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	executor := rehearse.NewExecutor(presubmitsToRehearse, prNumber, candidate, prRefs, r.dryRun, loggers, pjclient, prConfig.Prow.ProwJobNamespace)
	_, err = executor.ExecuteJobs(false)
	if err != nil {
		logger.WithError(err).Error("Failed to rehearse jobs")
		//return fmt.Errorf(rehearseFailureOutput)
	}

	return utilerrors.NewAggregate(errs)
}

func determineChangedTemplates(candidate, baseSHA, SHA string, prNumber int, configUpdaterCfg prowplugins.ConfigUpdater, logger *logrus.Entry) rehearse.ConfigMaps {
	var rehearsalTemplates rehearse.ConfigMaps
	changedTemplates, err := config.GetChangedTemplates(candidate, baseSHA)
	if err != nil {
		logger.WithError(err).Error("could not get template differences")
	}
	//TODO: going back to using SHA instead of buildID. The NewConfigMaps function will change to reflect that once original pj-rehearse is removed. See https://github.com/openshift/ci-tools/pull/996#discussion_r453704753.
	rehearsalTemplates, err = rehearse.NewConfigMaps(changedTemplates, "template", SHA, prNumber, configUpdaterCfg)
	if err != nil {
		logger.WithError(err).Error("could not match changed templates with cluster configmaps")
	}

	if len(rehearsalTemplates.Paths) != 0 {
		logger.WithField("templates", rehearsalTemplates.Paths).Info("templates changed")
	}

	return rehearsalTemplates
}

func determineChangedRegistrySteps(candidate, baseSHA string, logger *logrus.Entry) []registry.Node {
	var changedRegistrySteps []registry.Node
	refs, chains, workflows, _, _, _, err := load.Registry(filepath.Join(candidate, config.RegistryPath), load.RegistryFlag(0))
	if err != nil {
		logger.WithError(err).Error("could not load step registry")
	}
	graph, err := registry.NewGraph(refs, chains, workflows)
	if err != nil {
		logger.WithError(err).Error("could not create step registry graph")
	}
	changedRegistrySteps, err = config.GetChangedRegistrySteps(candidate, baseSHA, graph)
	if err != nil {
		logger.WithError(err).Error("could not get step registry differences")
	}
	if len(changedRegistrySteps) != 0 {
		var names []string
		for _, step := range changedRegistrySteps {
			names = append(names, step.Name())
		}
		logger.Infof("Found %d changed registry steps: %s", len(changedRegistrySteps), strings.Join(names, ", "))
	}

	return changedRegistrySteps
}

func determineChangedClusterProfiles(candidate, baseSHA, SHA string, prNumber int, configUpdaterCfg prowplugins.ConfigUpdater, logger *logrus.Entry) rehearse.ConfigMaps {
	var rehearsalClusterProfiles rehearse.ConfigMaps
	changedClusterProfiles, err := config.GetChangedClusterProfiles(candidate, baseSHA)
	if err != nil {
		logger.WithError(err).Error("could not get cluster profile differences")
	}
	//TODO: going back to using SHA instead of buildID. The NewConfigMaps function will change to reflect that once original pj-rehearse is removed. See https://github.com/openshift/ci-tools/pull/996#discussion_r453704753.
	rehearsalClusterProfiles, err = rehearse.NewConfigMaps(changedClusterProfiles, "cluster-profile", SHA, prNumber, configUpdaterCfg)
	if err != nil {
		logger.WithError(err).Error("could not match changed cluster profiles with cluster configmaps")
	}

	if len(rehearsalClusterProfiles.Paths) != 0 {
		logger.WithField("profiles", rehearsalClusterProfiles.Paths).Info("cluster profiles changed")
	}

	return rehearsalClusterProfiles
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
	prowJobClient ctrlruntimeclient.Client,
	podNamespace string,
	configUpdaterCfg prowplugins.ConfigUpdater,
	releaseRepoPath string,
	dryRun bool,
	changedTemplates rehearse.ConfigMaps,
	changedClusterProfiles rehearse.ConfigMaps,
	requiredImageStreamTags apihelper.ImageStreamTagMap,
) (cleanup, error) {
	buildClusters := sets.String{}
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
			buildClusters = sets.NewString("default")
		}
	}

	g, ctx := errgroup.WithContext(context.Background())
	for _, cluster := range buildClusters.UnsortedList() {
		buildCluster := cluster
		g.Go(func() error {
			log := log.WithField("buildCluster", buildCluster)
			clusterConfig := configs[buildCluster]
			cmClient, err := rehearse.NewCMClient(&clusterConfig, podNamespace, dryRun)
			if err != nil {
				log.WithError(err).Error("could not create a configMap client")
				//return errors.New(misconfigurationOutput)
			}

			cmManager := rehearse.NewCMManager(buildCluster, prowJobNamespace, cmClient, configUpdaterCfg, prNumber, releaseRepoPath, log)

			cleanupsLock.Lock()
			cleanups = append(cleanups, func() {
				if err := cmManager.Clean(); err != nil {
					log.WithError(err).Error("failed to clean up temporary ConfigMaps")
				}
			})
			cleanupsLock.Unlock()

			if err := cmManager.Create(changedTemplates); err != nil {
				log.WithError(err).Error("couldn't create temporary template ConfigMaps for rehearsals")
				//return errors.New(failedSetupOutput)
			}
			if err := cmManager.Create(changedClusterProfiles); err != nil {
				log.WithError(err).Error("couldn't create temporary cluster profile ConfigMaps for rehearsals")
				//return errors.New(failedSetupOutput)
			}

			if dryRun {
				return nil
			}
			config := configs[buildCluster]
			client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
			if err != nil {
				return fmt.Errorf("failed to construct client for cluster %s: %w", buildCluster, err)
			}

			if err := ensureImageStreamTags(ctx, client, requiredImageStreamTags, buildCluster, prowJobNamespace, prowJobClient, log); err != nil {
				return fmt.Errorf("failed to ensure imagestreamtags in cluster %s: %w", buildCluster, err)
			}

			return nil
		})
	}

	return cleanups.cleanup, g.Wait()
}

// Allow manipulating the speed of time for tests
var second = time.Second

func ensureImageStreamTags(ctx context.Context, client ctrlruntimeclient.Client, ists apihelper.ImageStreamTagMap, clusterName, namespace string, istImportClient ctrlruntimeclient.Client, log *logrus.Entry) error {
	if clusterName == appCIContextName {
		log.WithField("cluster", appCIContextName).Info("Not creating imports as its authoritative source for all imagestreams")
		return nil
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, ist := range ists {
		requiredImageStreamTag := ist
		g.Go(func() error {
			istLog := log.WithFields(logrus.Fields{"ist-namespace": requiredImageStreamTag.Namespace, "ist-name": requiredImageStreamTag.Name})
			err := client.Get(ctx, requiredImageStreamTag, &imagev1.ImageStreamTag{})
			if err == nil {
				istLog.Info("ImageStreamTag already exists in the build cluster")
				return nil
			}
			if !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
				return fmt.Errorf("failed to check if imagestreamtag %s exists: %w", requiredImageStreamTag, err)
			}
			istImport := &testimagestreamtagimportv1.TestImageStreamTagImport{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
					ClusterName: clusterName,
					Namespace:   requiredImageStreamTag.Namespace,
					Name:        requiredImageStreamTag.Name,
				},
			}
			istImport.SetDeterministicName()
			istLog.Info("Creating ImageStreamTagImport in the build cluster")
			if err := istImportClient.Create(ctx, istImport); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create imagestreamtag %s: %w", requiredImageStreamTag, err)
			}
			if err := wait.Poll(5*second, 30*second, func() (bool, error) {
				if err := client.Get(ctx, requiredImageStreamTag, &imagev1.ImageStreamTag{}); err != nil {
					if apierrors.IsNotFound(err) {
						return false, nil
					}
					return false, fmt.Errorf("get failed: %w", err)
				}
				return true, nil
			}); err != nil {
				return fmt.Errorf("failed waiting for imagestreamtag %s to appear: %w", requiredImageStreamTag, err)
			}

			return nil
		})
	}

	return g.Wait()
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
