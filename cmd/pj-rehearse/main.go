package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	prowgithub "k8s.io/test-infra/prow/github"
	prowplugins "k8s.io/test-infra/prow/plugins"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/rehearse"
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

func gatherOptions() options {
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

	fs.Parse(os.Args[1:])
	return o
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
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
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

	var clusterConfig *rest.Config
	var prowJobConfig *rest.Config
	if !o.dryRun {
		clusterConfig, err = clientconfig.GetConfig()
		if err != nil {
			logger.WithError(err).Error("could not load cluster clusterConfig")
			return fmt.Errorf(misconfigurationOutput)
		}
		prowJobConfig, err = pjKubeconfig(o.prowjobKubeconfig, clusterConfig)
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

	cmClient, err := rehearse.NewCMClient(clusterConfig, prConfig.Prow.PodNamespace, o.dryRun)
	if err != nil {
		logger.WithError(err).Error("could not create a configMap client")
		return fmt.Errorf(misconfigurationOutput)
	}

	cmManager := config.NewTemplateCMManager(prConfig.Prow.ProwJobNamespace, cmClient, pluginConfig, prNumber, o.releaseRepoPath, logger)
	defer func() {
		if err := cmManager.CleanupCMTemplates(); err != nil {
			logger.WithError(err).Error("failed to clean up temporary template CM")
		}
	}()
	if err := cmManager.CreateCMTemplates(changedTemplates); err != nil {
		logger.WithError(err).Error("couldn't create template configMap")
		return fmt.Errorf(failedSetupOutput)
	}
	if err := cmManager.CreateClusterProfiles(changedClusterProfiles); err != nil {
		logger.WithError(err).Error("couldn't create cluster profile ConfigMaps")
		return fmt.Errorf(failedSetupOutput)
	}

	pjclient, err := rehearse.NewProwJobClient(prowJobConfig, prConfig.Prow.ProwJobNamespace, o.dryRun)
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
	presubmitsToRehearse, err := jobConfigurer.ConfigurePresubmitRehearsals(toRehearse)
	if err != nil {
		return err
	}
	periodicsToRehearse, err := jobConfigurer.ConfigurePeriodicRehearsals(changedPeriodics)
	if err != nil {
		return err
	}

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

	presubmitsToRehearse = append(presubmitsToRehearse, jobConfigurer.ConvertPeriodicsToPresubmits(periodicsToRehearse)...)
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

	executor := rehearse.NewExecutor(presubmitsToRehearse, prNumber, o.releaseRepoPath, jobSpec.Refs, o.dryRun, loggers, pjclient)
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
