package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
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
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	prowgithub "k8s.io/test-infra/prow/github"
	prowplugins "k8s.io/test-infra/prow/plugins"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
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

type options struct {
	dryRun            bool
	debugLogPath      string
	prowjobKubeconfig string
	kubernetesOptions flagutil.KubernetesOptions
	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	releaseRepoPath string
	rehearsalLimit  int
}

func gatherOptions() (options, error) {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")

	fs.IntVar(&o.rehearsalLimit, "rehearsal-limit", 35, "Upper limit of jobs attempted to rehearse (if more jobs are being touched, only this many will be rehearsed)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	if len(o.releaseRepoPath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return o.kubernetesOptions.Validate(o.dryRun)
}

const (
	misconfigurationOutput = `ERROR: pj-rehearse: misconfiguration

pj-rehearse could not process its necessary inputs properly. No rehearsal
jobs were run. This is likely a pj-rehearse job configuration problem.`
	rehearseFailureOutput = `ERROR: pj-rehearse: rehearsal tool failure

pj-rehearse attempted to submit jobs for rehearsal, but it failed to either
submit them or to fetch their results. This is either a pj-rehearse bug or
an infrastructure issue.`
	jobsFailureOutput = `ERROR: pj-rehearse: rehearsed jobs failure

pj-rehearse rehearsed jobs and at least one of them failed. This means that
job would fail when executed against the current HEAD of the target branch.`
	failedSetupOutput = `ERROR: pj-rehearse: setup failure

pj-rehearse failed to finish all setup necessary to perform job rehearsals.
This is either a pj-rehearse bug or an infrastructure failure.`
	jobValidationOutput = `ERROR: pj-rehearse: failed to validate rehearsal jobs

pj-rehearse created invalid rehearsal jobs.This is either a pj-rehearse bug, or
the rehearsed jobs themselves are invalid.`

	appCIContextName = string(api.ClusterAPPCI)
)

func loadConfigUpdaterCfg(releaseRepoPath string) (ret prowplugins.ConfigUpdater, err error) {
	agent := prowplugins.ConfigAgent{}
	if err = agent.Load(filepath.Join(releaseRepoPath, config.PluginConfigInRepoPath), []string{filepath.Join(releaseRepoPath, filepath.Dir(config.PluginConfigInRepoPath))}, "_pluginconfig.yaml", true, false); err == nil {
		ret = agent.Config().ConfigUpdater
	}
	return
}

func rehearseMain() error {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("failed to register imagev1 scheme")
	}

	var jobSpec *pjdwapi.JobSpec
	if jobSpec, err = pjdwapi.ResolveSpecFromEnv(); err != nil {
		logrus.WithError(err).Error("could not read JOB_SPEC")
		return fmt.Errorf(misconfigurationOutput)
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)

	if jobSpec.Type != pjapi.PresubmitJob {
		logger.Info("Not able to rehearse jobs when not run in the context of a presubmit job")
		// Exiting successfully will make pj-rehearsal job not fail when run as a
		// in a batch job. Such failures would be confusing and unactionable
		return nil
	}

	org, repo, prNumber := jobSpec.Refs.Org, jobSpec.Refs.Repo, jobSpec.Refs.Pulls[0].Number
	logger.Infof("Rehearsing Prow jobs for configuration PR %s/%s#%d", org, repo, prNumber)

	buildClusterConfigs := map[string]rest.Config{}
	var prowJobConfig *rest.Config
	if !o.dryRun {
		buildClusterConfigs, err = o.kubernetesOptions.LoadClusterConfigs()
		if err != nil {
			logger.WithError(err).Error("failed to read kubeconfigs")
			return errors.New(misconfigurationOutput)
		}
		defaultKubeconfig := buildClusterConfigs[appCIContextName]
		prowJobConfig, err = pjKubeconfig(o.prowjobKubeconfig, &defaultKubeconfig)
		if err != nil {
			logger.WithError(err).Error("Could not load prowjob kubeconfig")
			return fmt.Errorf(misconfigurationOutput)
		}
	}

	prConfig := config.GetAllConfigs(o.releaseRepoPath, logger)
	configUpdaterCfg, err := loadConfigUpdaterCfg(o.releaseRepoPath)
	if err != nil {
		logger.WithError(err).Error("could not load plugin configuration from tested revision of release repo")
		return fmt.Errorf(misconfigurationOutput)
	}
	masterConfig, err := config.GetAllConfigsFromSHA(o.releaseRepoPath, jobSpec.Refs.BaseSHA, logger)
	if err != nil {
		logger.WithError(err).Error("could not load configuration from base revision of release repo")
		return fmt.Errorf(misconfigurationOutput)
	}

	// We always need both Prow config versions, otherwise we cannot compare them
	if masterConfig.Prow == nil || prConfig.Prow == nil {
		logger.WithError(err).Error("could not load Prow configs from base or tested revision of release repo")
		return fmt.Errorf(misconfigurationOutput)
	}
	// We always need PR versions of ciop config, otherwise we cannot provide them to rehearsed jobs
	if prConfig.CiOperator == nil {
		logger.WithError(err).Error("could not load ci-operator configs from tested revision of release repo")
		return fmt.Errorf(misconfigurationOutput)
	}

	// We can only detect changes if we managed to load both ci-operator config versions
	changedCiopConfigData := config.DataByFilename{}
	affectedJobs := make(map[string]sets.String)
	if masterConfig.CiOperator != nil && prConfig.CiOperator != nil {
		data, jobs := diffs.GetChangedCiopConfigs(masterConfig.CiOperator, prConfig.CiOperator, logger)
		changedCiopConfigData = data
		affectedJobs = jobs
	}

	var changedRegistrySteps []registry.Node
	var refs registry.ReferenceByName
	var chains registry.ChainByName
	var workflows registry.WorkflowByName
	var observers registry.ObserverByName

	if !o.noRegistry {
		refs, chains, workflows, _, _, observers, err = load.Registry(filepath.Join(o.releaseRepoPath, config.RegistryPath), load.RegistryFlag(0))
		if err != nil {
			logger.WithError(err).Error("could not load step registry")
			return fmt.Errorf(misconfigurationOutput)
		}
		graph, err := registry.NewGraph(refs, chains, workflows)
		if err != nil {
			logger.WithError(err).Error("could not create step registry graph")
			return fmt.Errorf(misconfigurationOutput)
		}
		changedRegistrySteps, err = config.GetChangedRegistrySteps(o.releaseRepoPath, jobSpec.Refs.BaseSHA, graph)
		if err != nil {
			logger.WithError(err).Error("could not get step registry differences")
			return fmt.Errorf(misconfigurationOutput)
		}
	}
	if len(changedRegistrySteps) != 0 {
		var names []string
		for _, step := range changedRegistrySteps {
			names = append(names, step.Name())
		}
		logger.Infof("Found %d changed registry steps: %s", len(changedRegistrySteps), strings.Join(names, ", "))
	}

	var rehearsalTemplates rehearse.ConfigMaps
	if !o.noTemplates {
		changedTemplates, err := config.GetChangedTemplates(o.releaseRepoPath, jobSpec.Refs.BaseSHA)
		if err != nil {
			logger.WithError(err).Error("could not get template differences")
			return fmt.Errorf(misconfigurationOutput)
		}
		rehearsalTemplates, err = rehearse.NewConfigMaps(changedTemplates, "template", jobSpec.BuildID, prNumber, configUpdaterCfg)
		if err != nil {
			logger.WithError(err).Error("could not match changed templates with cluster configmaps")
			return fmt.Errorf(misconfigurationOutput)
		}

	}
	if len(rehearsalTemplates.Paths) != 0 {
		logger.WithField("templates", rehearsalTemplates.Paths).Info("templates changed")
	}

	var rehearsalClusterProfiles rehearse.ConfigMaps
	if !o.noClusterProfiles {
		changedClusterProfiles, err := config.GetChangedClusterProfiles(o.releaseRepoPath, jobSpec.Refs.BaseSHA)
		if err != nil {
			logger.WithError(err).Error("could not get cluster profile differences")
			return fmt.Errorf(misconfigurationOutput)
		}
		rehearsalClusterProfiles, err = rehearse.NewConfigMaps(changedClusterProfiles, "cluster-profile", jobSpec.BuildID, prNumber, configUpdaterCfg)
		if err != nil {
			logger.WithError(err).Error("could not match changed cluster profiles with cluster configmaps")
			return fmt.Errorf(misconfigurationOutput)
		}
	}
	if len(rehearsalClusterProfiles.Paths) != 0 {
		logger.WithField("profiles", rehearsalClusterProfiles.Paths).Info("cluster profiles changed")
	}

	pjclient, err := rehearse.NewProwJobClient(prowJobConfig, o.dryRun)
	if err != nil {
		logger.WithError(err).Error("could not create a ProwJob client")
		return fmt.Errorf(failedSetupOutput)
	}

	debugLogger := logrus.New()
	debugLogger.Level = logrus.DebugLevel
	if o.debugLogPath != "" {
		if f, err := os.OpenFile(o.debugLogPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, os.ModePerm); err == nil {
			defer f.Close()
			debugLogger.Out = f
		} else {
			logger.WithError(err).Error("could not open debug log file")
			return fmt.Errorf(failedSetupOutput)
		}
	}
	loggers := rehearse.Loggers{Job: logger, Debug: debugLogger.WithField(prowgithub.PrLogField, prNumber)}
	toRehearse := config.Presubmits{}
	periodicsToRehearse := config.Periodics{}

	changedPeriodics := diffs.GetChangedPeriodics(masterConfig.Prow, prConfig.Prow, logger)
	periodicsToRehearse.AddAll(changedPeriodics, config.ChangedPeriodic)
	changedPresubmits := diffs.GetChangedPresubmits(masterConfig.Prow, prConfig.Prow, logger)
	toRehearse.AddAll(changedPresubmits, config.ChangedPresubmit)

	presubmitsForCiopConfigs, periodicsForCiopConfigs := diffs.GetJobsForCiopConfigs(prConfig.Prow, changedCiopConfigData, affectedJobs, logger)
	toRehearse.AddAll(presubmitsForCiopConfigs, config.ChangedCiopConfig)
	periodicsToRehearse.AddAll(periodicsForCiopConfigs, config.ChangedCiopConfig)

	presubmitsForClusterProfiles := diffs.GetPresubmitsForClusterProfiles(prConfig.Prow, rehearsalClusterProfiles.ProductionNames, logger)
	toRehearse.AddAll(presubmitsForClusterProfiles, config.ChangedClusterProfile)

	randomJobsForChangedTemplates := rehearse.AddRandomJobsForChangedTemplates(rehearsalTemplates.ProductionNames, toRehearse, prConfig.Prow.JobConfig.PresubmitsStatic, loggers)
	toRehearse.AddAll(randomJobsForChangedTemplates, config.ChangedTemplate)

	presubmitsForRegistry, periodicsForRegistry := rehearse.SelectJobsForChangedRegistry(changedRegistrySteps, prConfig.Prow.JobConfig.PresubmitsStatic, prConfig.Prow.JobConfig.Periodics, prConfig.CiOperator, loggers)
	toRehearse.AddAll(presubmitsForRegistry, config.ChangedRegistryContent)
	periodicsToRehearse.AddAll(periodicsForRegistry, config.ChangedRegistryContent)

	resolver := registry.NewResolver(refs, chains, workflows, observers)
	jobConfigurer := rehearse.NewJobConfigurer(prConfig.CiOperator, prConfig.Prow, resolver, prNumber, loggers, rehearsalTemplates.Names, rehearsalClusterProfiles.Names, jobSpec.Refs)
	imagestreamtags, presubmitsToRehearse, err := jobConfigurer.ConfigurePresubmitRehearsals(toRehearse)
	if err != nil {
		return err
	}

	periodicImageStreamTags, periodics, err := jobConfigurer.ConfigurePeriodicRehearsals(periodicsToRehearse)
	if err != nil {
		return err
	}
	apihelper.MergeImageStreamTagMaps(imagestreamtags, periodicImageStreamTags)

	periodicPresubmits, err := jobConfigurer.ConvertPeriodicsToPresubmits(periodics)
	if err != nil {
		return err
	}
	presubmitsToRehearse = append(presubmitsToRehearse, periodicPresubmits...)

	if rehearsals := len(presubmitsToRehearse); rehearsals == 0 {
		logger.Info("no jobs to rehearse have been found")
		return nil
	} else if rehearsals > o.rehearsalLimit {
		jobCountFields := logrus.Fields{
			"rehearsal-threshold": o.rehearsalLimit,
			"rehearsal-jobs":      rehearsals,
		}
		logger.WithFields(jobCountFields).Info("Would rehearse too many jobs, selecting a subset")
		presubmitsToRehearse = determineSubsetToRehearse(presubmitsToRehearse, o.rehearsalLimit)
	}

	if prConfig.Prow.JobConfig.PresubmitsStatic == nil {
		prConfig.Prow.JobConfig.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
	}
	for _, presubmit := range presubmitsToRehearse {

		// We can only have a given repo once, so remove whats in Refs from ExtraRefs
		var cleanExtraRefs []pjapi.Refs
		for _, extraRef := range presubmit.ExtraRefs {
			if extraRef.Org == org && extraRef.Repo == repo {
				continue
			}
			cleanExtraRefs = append(cleanExtraRefs, extraRef)
		}
		presubmit.ExtraRefs = cleanExtraRefs

		prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo] = append(prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo], *presubmit)
	}
	if err := prConfig.Prow.ValidateJobConfig(); err != nil {
		logger.WithError(err).Error("jobconfig validation failed")
		return fmt.Errorf(jobValidationOutput)
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
		o.releaseRepoPath,
		o.dryRun,
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

	executor := rehearse.NewExecutor(presubmitsToRehearse, prNumber, o.releaseRepoPath, jobSpec.Refs, o.dryRun, loggers, pjclient, prConfig.Prow.ProwJobNamespace)
	success, err := executor.ExecuteJobs()
	if err != nil {
		logger.WithError(err).Error("Failed to rehearse jobs")
		return fmt.Errorf(rehearseFailureOutput)
	}
	if !success {
		logger.Error("Some jobs failed their rehearsal runs")
		return fmt.Errorf(jobsFailureOutput)
	} else {
		logger.Info("All jobs were rehearsed successfully")
	}
	return utilerrors.NewAggregate(errs)
}

func main() {
	if err := rehearseMain(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
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
				return errors.New(misconfigurationOutput)
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
				return errors.New(failedSetupOutput)
			}
			if err := cmManager.Create(changedClusterProfiles); err != nil {
				log.WithError(err).Error("couldn't create temporary cluster profile ConfigMaps for rehearsals")
				return errors.New(failedSetupOutput)
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
