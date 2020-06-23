package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	prowgithub "k8s.io/test-infra/prow/github"
	prowplugins "k8s.io/test-infra/prow/plugins"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	dryRun            bool
	local             bool
	debugLogPath      string
	prowjobKubeconfig string

	noTemplates       bool
	noRegistry        bool
	noClusterProfiles bool

	releaseRepoPath string
	rehearsalLimit  int
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.CommandLine

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")
	fs.BoolVar(&o.local, "local", false, "Whether this is a local execution or part of a CI job")

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	fs.StringVar(&o.prowjobKubeconfig, "prowjob-kubeconfig", "", "Path to the prowjob kubeconfig. If unset, default kubeconfig will be used for prowjobs.")

	fs.BoolVar(&o.noTemplates, "no-templates", false, "If true, do not attempt to compare templates")
	fs.BoolVar(&o.noRegistry, "no-registry", false, "If true, do not attempt to compare step registry content")
	fs.BoolVar(&o.noClusterProfiles, "no-cluster-profiles", false, "If true, do not attempt to compare cluster profiles")

	fs.IntVar(&o.rehearsalLimit, "rehearsal-limit", 15, "Upper limit of jobs attempted to rehearse (if more jobs would be rehearsed, none will)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %v", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	if len(o.releaseRepoPath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return nil
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
)

func loadPluginConfig(releaseRepoPath string) (ret prowplugins.ConfigUpdater, err error) {
	agent := prowplugins.ConfigAgent{}
	if err = agent.Load(filepath.Join(releaseRepoPath, config.PluginConfigInRepoPath), true); err == nil {
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
	if o.local {
		if jobSpec, err = config.NewLocalJobSpec(o.releaseRepoPath); err != nil {
			logrus.WithError(err).Error("could not create local JobSpec")
			return fmt.Errorf(misconfigurationOutput)
		}
	} else {
		if jobSpec, err = pjdwapi.ResolveSpecFromEnv(); err != nil {
			logrus.WithError(err).Error("could not read JOB_SPEC")
			return fmt.Errorf(misconfigurationOutput)
		}
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)

	if jobSpec.Type != pjapi.PresubmitJob {
		logger.Info("Not able to rehearse jobs when not run in the context of a presubmit job")
		// Exiting successfully will make pj-rehearsal job not fail when run as a
		// in a batch job. Such failures would be confusing and unactionable
		return nil
	}

	if o.local {
		jobSpec.Refs.Pulls[0].Number = int(time.Now().Unix())
	}
	org, repo, prNumber := jobSpec.Refs.Org, jobSpec.Refs.Repo, jobSpec.Refs.Pulls[0].Number
	logger.Infof("Rehearsing Prow jobs for configuration PR %s/%s#%d", org, repo, prNumber)

	buildClusterConfigs := map[string]*rest.Config{}
	var prowJobConfig *rest.Config
	if !o.dryRun {
		// Only the env var allows to supply multiple kubeconfigs
		if _, exists := os.LookupEnv("KUBECONFIG"); exists {
			buildClusterConfigs, _, err = util.LoadKubeConfigs("")
			if err != nil {
				logger.WithError(err).Error("failed to read kubeconfigs")
				return errors.New(misconfigurationOutput)
			}
		}
		if _, hasAPICIKubeconfig := buildClusterConfigs["api.ci"]; !hasAPICIKubeconfig {
			apiCIConfig, err := rest.InClusterConfig()
			if err != nil {
				logger.WithError(err).Error("could not load cluster clusterConfig")
				return fmt.Errorf(misconfigurationOutput)
			}
			logger.Info("Got api.ci kubeconfig via in-cluster")
			buildClusterConfigs["api.ci"] = apiCIConfig
		}
		prowJobConfig, err = pjKubeconfig(o.prowjobKubeconfig, buildClusterConfigs["api.ci"])
		if err != nil {
			logger.WithError(err).Error("Could not load prowjob kubeconfig")
			return fmt.Errorf(misconfigurationOutput)
		}
	}

	prConfig := config.GetAllConfigs(o.releaseRepoPath, logger)
	pluginConfig, err := loadPluginConfig(o.releaseRepoPath)
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
	var graph registry.NodeByName

	if !o.noRegistry {
		refs, chains, workflows, _, err = load.Registry(filepath.Join(o.releaseRepoPath, config.RegistryPath), false)
		if err != nil {
			logger.WithError(err).Error("could not load step registry")
			return fmt.Errorf(misconfigurationOutput)
		}
		graph, err = registry.NewGraph(refs, chains, workflows)
		if err != nil {
			logger.WithError(err).Error("could not create step registry graph")
			return fmt.Errorf(misconfigurationOutput)
		}
		changedRegistrySteps, err = config.GetChangedRegistrySteps(o.releaseRepoPath, jobSpec.Refs.BaseSHA, graph)
		if err != nil {
			logger.WithError(err).Error("could not get step registry differences")
			return fmt.Errorf(misconfigurationOutput)
		}
	} else {
		graph, err = registry.NewGraph(refs, chains, workflows)
		if err != nil {
			logger.WithError(err).Error("could not create step registry graph")
			return fmt.Errorf(misconfigurationOutput)
		}
	}
	if len(changedRegistrySteps) != 0 {
		var names []string
		for _, step := range changedRegistrySteps {
			names = append(names, step.Name())
		}
		logger.Infof("found %d changed registry steps: %s", len(changedRegistrySteps), strings.Join(names, ", "))
	}

	var changedTemplates []config.ConfigMapSource
	if !o.noTemplates {
		changedTemplates, err = config.GetChangedTemplates(o.releaseRepoPath, jobSpec.Refs.BaseSHA)
		if err != nil {
			logger.WithError(err).Error("could not get template differences")
			return fmt.Errorf(misconfigurationOutput)
		}
	}
	if len(changedTemplates) != 0 {
		logger.WithField("templates", changedTemplates).Info("templates changed")
	}

	var changedClusterProfiles []config.ConfigMapSource
	if !o.noClusterProfiles {
		changedClusterProfiles, err = config.GetChangedClusterProfiles(o.releaseRepoPath, jobSpec.Refs.BaseSHA)
		if err != nil {
			logger.WithError(err).Error("could not get cluster profile differences")
			return fmt.Errorf(misconfigurationOutput)
		}
	}
	if len(changedClusterProfiles) != 0 {
		logger.WithField("profiles", changedClusterProfiles).Info("cluster profiles changed")
	}

	if o.local {
		prConfig.Prow.ProwJobNamespace = config.StagingNamespace
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

	changedPeriodics := diffs.GetChangedPeriodics(masterConfig.Prow, prConfig.Prow, logger)
	toRehearse := diffs.GetChangedPresubmits(masterConfig.Prow, prConfig.Prow, logger)

	presubmitsWithChangedCiopConfigs := diffs.GetPresubmitsForCiopConfigs(prConfig.Prow, changedCiopConfigData, affectedJobs, logger)
	toRehearse.AddAll(presubmitsWithChangedCiopConfigs)

	presubmitsWithChangedTemplates := rehearse.AddRandomJobsForChangedTemplates(changedTemplates, toRehearse, prConfig.Prow.JobConfig.PresubmitsStatic, loggers)
	toRehearse.AddAll(presubmitsWithChangedTemplates)

	toRehearseClusterProfiles := diffs.GetPresubmitsForClusterProfiles(prConfig.Prow, changedClusterProfiles, logger)
	toRehearse.AddAll(toRehearseClusterProfiles)

	presubmitsWithChangedRegistry := rehearse.AddRandomJobsForChangedRegistry(changedRegistrySteps, graph, prConfig.Prow.JobConfig.PresubmitsStatic, filepath.Join(o.releaseRepoPath, config.CiopConfigInRepoPath), loggers)
	toRehearse.AddAll(presubmitsWithChangedRegistry)

	resolver := registry.NewResolver(refs, chains, workflows)
	jobConfigurer := rehearse.NewJobConfigurer(prConfig.CiOperator, resolver, prNumber, loggers, changedTemplates, changedClusterProfiles, jobSpec.Refs)
	imagestreamtags, presubmitsToRehearse, err := jobConfigurer.ConfigurePresubmitRehearsals(toRehearse)
	if err != nil {
		return err
	}
	periodicImageStreamTags, periodicsToRehearse, err := jobConfigurer.ConfigurePeriodicRehearsals(changedPeriodics)
	if err != nil {
		return err
	}
	apihelper.MergeImageStreamTagMaps(imagestreamtags, periodicImageStreamTags)

	rehearsals := len(presubmitsToRehearse) + len(periodicsToRehearse)
	if rehearsals == 0 {
		logger.Info("no jobs to rehearse have been found")
		return nil
	} else if rehearsals > o.rehearsalLimit {
		jobCountFields := logrus.Fields{
			"rehearsal-threshold": o.rehearsalLimit,
			"rehearsal-jobs":      rehearsals,
		}
		logger.WithFields(jobCountFields).Info("Would rehearse too many jobs, will not proceed")
		return nil
	}

	periodicPresubmits, err := jobConfigurer.ConvertPeriodicsToPresubmits(periodicsToRehearse)
	if err != nil {
		return err
	}
	presubmitsToRehearse = append(presubmitsToRehearse, periodicPresubmits...)
	if prConfig.Prow.JobConfig.PresubmitsStatic == nil {
		prConfig.Prow.JobConfig.PresubmitsStatic = map[string][]prowconfig.Presubmit{}
	}
	for _, presubmit := range presubmitsToRehearse {
		prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo] = append(prConfig.Prow.JobConfig.PresubmitsStatic[org+"/"+repo], *presubmit)
	}
	if err := prConfig.Prow.ValidateJobConfig(); err != nil {
		logger.WithError(err).Error("jobconfig validation failed")
		return fmt.Errorf(jobValidationOutput)
	}

	cleanup, err := setupDependencies(
		presubmitsToRehearse,
		prNumber,
		buildClusterConfigs,
		logger,
		prConfig.Prow.ProwJobNamespace,
		pjclient,
		prConfig.Prow.PodNamespace,
		pluginConfig,
		o.releaseRepoPath,
		o.dryRun,
		changedTemplates,
		changedClusterProfiles,
		imagestreamtags)
	if err != nil {
		logger.WithError(err).Error("Failed to set up dependencies")
		return errors.New(failedSetupOutput)
	}
	defer cleanup()

	executor := rehearse.NewExecutor(presubmitsToRehearse, prNumber, o.releaseRepoPath, jobSpec.Refs, o.dryRun, loggers, pjclient, prConfig.Prow.ProwJobNamespace)
	success, err := executor.ExecuteJobs()
	if err != nil {
		logger.WithError(err).Error("Failed to rehearse jobs")
		return fmt.Errorf(rehearseFailureOutput)
	}
	if !success {
		logger.Error("Some jobs failed their rehearsal runs")
		return fmt.Errorf(jobsFailureOutput)
	}
	logger.Info("All jobs were rehearsed successfully")
	return nil
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
	configs map[string]*rest.Config,
	log *logrus.Entry,
	prowJobNamespace string,
	prowJobClient ctrlruntimeclient.Client,
	podNamespace string,
	pluginConfig prowplugins.ConfigUpdater,
	releaseRepoPath string,
	dryRun bool,
	changedTemplates []config.ConfigMapSource,
	changedClusterProfiles []config.ConfigMapSource,
	requiredImageStreamTags apihelper.ImageStreamTagMap,
) (_ cleanup, retErr error) {
	buildClusters := sets.String{}
	for _, job := range jobs {
		if _, ok := configs[job.Cluster]; !ok && !dryRun {
			return nil, fmt.Errorf("no config for buildcluster %s provided", job.Cluster)
		}
		buildClusters.Insert(job.Cluster)
	}

	var cleanups cleanups
	cleanupsLock := &sync.Mutex{}
	defer func() {
		if retErr != nil {
			cleanups.cleanup()
		}
	}()

	g, ctx := errgroup.WithContext(context.Background())
	for _, buildCluster := range buildClusters.UnsortedList() {
		g.Go(func() error {
			log := log.WithField("buildCluster", buildCluster)
			cmClient, err := rehearse.NewCMClient(configs[buildCluster], podNamespace, dryRun)
			if err != nil {
				log.WithError(err).Error("could not create a configMap client")
				return errors.New(misconfigurationOutput)
			}

			cmManager := config.NewTemplateCMManager(prowJobNamespace, cmClient, pluginConfig, prNumber, releaseRepoPath, log)

			cleanupsLock.Lock()
			cleanups = append(cleanups, func() {
				if err := cmManager.CleanupCMTemplates(); err != nil {
					log.WithError(err).Error("failed to clean up temporary template CM")
				}
			})
			cleanupsLock.Unlock()

			if err := cmManager.CreateCMTemplates(changedTemplates); err != nil {
				log.WithError(err).Error("couldn't create template configMap")
				return errors.New(failedSetupOutput)
			}
			if err := cmManager.CreateClusterProfiles(changedClusterProfiles); err != nil {
				log.WithError(err).Error("couldn't create cluster profile ConfigMaps")
				return errors.New(failedSetupOutput)
			}

			if dryRun {
				return nil
			}

			client, err := ctrlruntimeclient.New(configs[buildCluster], ctrlruntimeclient.Options{})
			if err != nil {
				return fmt.Errorf("failed to construct client for cluster %s: %w", buildCluster, err)
			}

			if err := ensureImageStreamTags(ctx, client, requiredImageStreamTags, buildCluster, prowJobNamespace, prowJobClient); err != nil {
				return fmt.Errorf("failed to ensure imagestreamtags in cluster %s: %w", buildCluster, err)
			}

			return nil
		})
	}

	return cleanups.cleanup, g.Wait()
}

// Allow manipulating the speed of time for tests
var second = time.Second

func ensureImageStreamTags(ctx context.Context, client ctrlruntimeclient.Client, ists apihelper.ImageStreamTagMap, clusterName, namespace string, istImportClient ctrlruntimeclient.Client) error {

	g, ctx := errgroup.WithContext(ctx)

	for _, requiredImageStreamTag := range ists {
		g.Go(func() error {
			requiredImageStreamTag := requiredImageStreamTag
			err := client.Get(ctx, requiredImageStreamTag, &imagev1.ImageStreamTag{})
			if err == nil {
				return nil
			}
			if !apierrors.IsNotFound(err) {
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
