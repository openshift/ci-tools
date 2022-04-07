package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	githubOrg      = "openshift"
	githubRepo     = "release"
	githubLogin    = "openshift-bot"
	matchTitle     = "Automate prow job dispatcher"
	upstreamBranch = "master"
)

type options struct {
	prowJobConfigDir string
	configPath       string

	maxConcurrency       int
	prometheusDaysBefore int

	createPR    bool
	githubLogin string
	targetDir   string
	assign      string

	enableClusters  flagutil.Strings
	disableClusters flagutil.Strings
	defaultCluster  string

	bumper.GitAuthorOptions
	dispatcher.PrometheusOptions
	prcreation.PRCreationOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	fs.StringVar(&o.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	fs.IntVar(&o.prometheusDaysBefore, "prometheus-days-before", 1, "Number [1,15] of days before. Time 00-00-00 of that day will be used as time to query Prometheus. E.g., 1 means 00-00-00 of yesterday.")
	fs.IntVar(&o.maxConcurrency, "concurrency", 0, "Maximum number of concurrent in-flight goroutines to handle files.")

	fs.BoolVar(&o.createPR, "create-pr", false, "Create a pull request to the change made with this tool.")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", "ghost", "The github username or group name to assign the created pull request to.")

	fs.Var(&o.enableClusters, "enable-cluster", "Enable this cluster. Does nothing if the cluster is enabled. Can be passed multiple times and must be disjoint with all --disable-cluster values.")
	fs.Var(&o.disableClusters, "disable-cluster", "Disable this cluster. Does nothing if the cluster is disabled. Can be passed multiple times and must be disjoint with all --enable-cluster values.")
	fs.StringVar(&o.defaultCluster, "default-cluster", "", "If passed, changes the default cluster to the specified value.")

	o.GitAuthorOptions.AddFlags(fs)
	o.PrometheusOptions.AddFlags(fs)
	o.PRCreationOptions.AddFlags(fs)

	o.AllowAnonymous = true
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func (o *options) validate() error {
	if o.maxConcurrency == 0 {
		o.maxConcurrency = runtime.GOMAXPROCS(0)
	}
	if o.prowJobConfigDir == "" {
		return fmt.Errorf("mandatory argument --prow-jobs-dir wasn't set")
	}
	if o.configPath == "" {
		return fmt.Errorf("mandatory argument --config-path wasn't set")
	}

	if o.prometheusDaysBefore < 1 || o.prometheusDaysBefore > 15 {
		return fmt.Errorf("--prometheus-days-before must be between 1 and 15")
	}

	enabled := o.enableClusters.StringSet()
	disabled := o.disableClusters.StringSet()
	if enabled.Intersection(disabled).Len() > 0 {
		return fmt.Errorf("--enable-cluster and --disable-cluster values must be disjoint sets")
	}

	if disabled.Has(o.defaultCluster) {
		return fmt.Errorf("--default-cluster value cannot be also be in --disable-cluster")
	}

	if o.createPR {
		if o.githubLogin == "" {
			return fmt.Errorf("--github-login cannot be empty string")
		}
		if err := o.GitAuthorOptions.Validate(); err != nil {
			return err
		}
		if o.targetDir == "" {
			return fmt.Errorf("--target-dir is mandatory")
		}
		if o.assign == "" {
			return fmt.Errorf("--assign is mandatory")
		}
		if err := o.PRCreationOptions.GitHubOptions.Validate(false); err != nil {
			return err
		}
	}
	return o.PrometheusOptions.Validate()
}

// getCloudProvidersForE2ETests returns a set of cloud providers where a cluster is hosted for an e2e test defined in the given Prow job config.
func getCloudProvidersForE2ETests(jc *prowconfig.JobConfig) sets.String {
	cloudProviders := sets.NewString()
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			if cloud := dispatcher.DetermineCloud(job.JobBase); cloud != "" {
				cloudProviders.Insert(cloud)
			}
		}
	}
	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			if cloud := dispatcher.DetermineCloud(job.JobBase); cloud != "" {
				cloudProviders.Insert(cloud)
			}
		}
	}
	for _, job := range jc.Periodics {
		if cloud := dispatcher.DetermineCloud(job.JobBase); cloud != "" {
			cloudProviders.Insert(cloud)
		}
	}
	return cloudProviders
}

type clusterVolume struct {
	// [cloudProvider][cluster]volume
	clusterVolumeMap map[string]map[string]float64
	// only needed for stable tests: traverse the above map by sorted key list
	cloudProviders sets.String
	mutex          sync.Mutex
}

// findClusterForJobConfig finds a cluster running on a preferred cloud provider for the jobs in a Prow job config.
// The chosen cluster will be the one with minimal workload with the given cloud provider.
// If the cluster provider is empty string, it will choose the one with minimal workload across all cloud providers.
func (cv *clusterVolume) findClusterForJobConfig(cloudProvider string, jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) (string, error) {
	// no cluster in the build farm is from the targeting cloud provider
	if _, ok := cv.clusterVolumeMap[cloudProvider]; !ok {
		cloudProvider = ""
	}
	var cluster, rCloudProvider string
	min := float64(-1)
	cv.mutex.Lock()
	for _, cp := range cv.cloudProviders.List() {
		m := cv.clusterVolumeMap[cp]
		for c, v := range m {
			if cloudProvider == "" || cloudProvider == cp {
				if min < 0 || min > v {
					min = v
					cluster = c
					rCloudProvider = cp
				}
			}
		}
	}

	var errs []error
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			if err := cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes); err != nil {
				errs = append(errs, err)
			}
		}
	}

	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			if err := cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, job := range jc.Periodics {
		if err := cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes); err != nil {
			errs = append(errs, err)
		}
	}

	cv.mutex.Unlock()
	return cluster, utilerrors.NewAggregate(errs)
}

func (cv *clusterVolume) addToVolume(cloudProvider, cluster string, jobBase prowconfig.JobBase, path string, config *dispatcher.Config, jobVolumes map[string]float64) error {
	determinedCluster, canBeRelocated, err := config.DetermineClusterForJob(jobBase, path)
	if err != nil {
		return fmt.Errorf("failed to determine cluster for the job %s in path %q: %w", jobBase.Name, path, err)
	}
	if cluster == string(determinedCluster) || canBeRelocated {
		cv.clusterVolumeMap[cloudProvider][cluster] = cv.clusterVolumeMap[cloudProvider][cluster] + jobVolumes[jobBase.Name]
	} else if determinedCloudProvider := config.IsInBuildFarm(determinedCluster); determinedCloudProvider != "" {
		cv.clusterVolumeMap[string(determinedCloudProvider)][string(determinedCluster)] = cv.clusterVolumeMap[string(determinedCloudProvider)][string(determinedCluster)] + jobVolumes[jobBase.Name]
	}
	return nil
}

// dispatchJobConfig dispatches the jobs defined in a Prow jon config
func (cv *clusterVolume) dispatchJobConfig(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) (string, error) {
	cloudProvidersForE2ETests := getCloudProvidersForE2ETests(jc)
	var cloudProvider, cluster string
	var err error
	if cloudProvidersForE2ETests.Len() == 1 {
		cloudProvider, _ = cloudProvidersForE2ETests.PopAny()
	}
	if cluster, err = cv.findClusterForJobConfig(cloudProvider, jc, path, config, jobVolumes); err != nil {
		return "", fmt.Errorf("fail to find cluster for job config: %w", err)
	}
	return cluster, nil
}

type configResult struct {
	cluster  string
	filename string
	path     string
}

// dispatchJobs loads the Prow jobs and chooses a cluster in the build farm if possible.
// The current implementation walks through the Prow Job config files.
// For each file, it tries to assign all jobs in it to a cluster in the build farm.
//  - When all the e2e tests are targeting the same cloud provider, we run the test pod on the that cloud provider too.
//  - When the e2e tests are targeting different cloud providers, or there is no e2e tests at all, we can run the tests
//    on any cluster in the build farm. Those jobs are used to load balance the workload of clusters in the build farm.
func dispatchJobs(ctx context.Context, prowJobConfigDir string, maxConcurrency int, config *dispatcher.Config, jobVolumes map[string]float64) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	disabledClusters := map[api.Cloud][]api.Cluster{}

	// cv stores the volume for each cluster in the build farm
	cv := &clusterVolume{clusterVolumeMap: map[string]map[string]float64{}, cloudProviders: sets.NewString()}
	for cloudProvider, v := range config.BuildFarm {
		for cluster, cfg := range v {
			if cfg.Disabled {
				if cluster == config.Default {
					return fmt.Errorf("Default cluster %s is disabled", cluster)
				}
				disabledClusters[cloudProvider] = append(disabledClusters[cloudProvider], cluster)
				delete(config.BuildFarm[cloudProvider], cluster)
				continue
			}

			cloudProviderString := string(cloudProvider)
			if _, ok := cv.clusterVolumeMap[cloudProviderString]; !ok {
				cv.clusterVolumeMap[cloudProviderString] = map[string]float64{}
			}
			cv.clusterVolumeMap[cloudProviderString][string(cluster)] = 0
		}
		if len(cv.clusterVolumeMap) > 0 {
			cv.cloudProviders.Insert(string(cloudProvider))
		}
	}

	// no clusters in the build farm
	if len(cv.clusterVolumeMap) == 0 {
		return nil
	}

	sem := semaphore.NewWeighted(int64(maxConcurrency))
	objChan := make(chan interface{})
	var errs []error
	results := map[string][]string{}

	readingDone := make(chan struct{})
	go func() {
		for o := range objChan {
			switch o := o.(type) {
			case configResult:
				if !config.MatchingPathRegEx(o.path) {
					results[o.cluster] = append(results[o.cluster], o.filename)
				}
			case error:
				errs = append(errs, o)
			default:
				// this should never happen
				logrus.Errorf("Received unknown type %T of o with value %v", o, o)
			}
		}
		close(readingDone)
	}()

	if err := filepath.WalkDir(prowJobConfigDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			objChan <- fmt.Errorf("failed to walk file/directory '%s'", path)
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		if err := sem.Acquire(ctx, 1); err != nil {
			objChan <- fmt.Errorf("failed to acquire semaphore for path %s: %w", path, err)
			return nil
		}
		go func(path string) {
			defer sem.Release(1)

			data, err := gzip.ReadFileMaybeGZIP(path)
			if err != nil {
				objChan <- fmt.Errorf("failed to read file %q: %w", path, err)
				return
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				objChan <- fmt.Errorf("failed to unmarshal file %q: %w", path, err)
				return
			}

			cluster, err := cv.dispatchJobConfig(jobConfig, path, config, jobVolumes)
			if err != nil {
				objChan <- fmt.Errorf("failed to dispatch job config %q: %w", path, err)
			}
			objChan <- configResult{cluster: cluster, path: path, filename: info.Name()}
		}(path)

		return nil
	}); err != nil {
		return fmt.Errorf("failed to dispatch all Prow jobs: %w", err)
	}

	if err := sem.Acquire(ctx, int64(maxConcurrency)); err != nil {
		objChan <- fmt.Errorf("failed to acquire semaphore while wating all workers to finish: %w", err)
	}
	close(objChan)
	<-readingDone

	for cloudProvider, m := range cv.clusterVolumeMap {
		for cluster, volume := range m {
			logrus.WithField("cloudProvider", cloudProvider).WithField("cluster", cluster).WithField("volume", volume).Info("dispatched the volume on the cluster")
		}
	}

	for cloudProvider, jobGroups := range config.BuildFarm {
		for cluster := range jobGroups {
			config.BuildFarm[cloudProvider][cluster] = &dispatcher.BuildFarmConfig{FilenamesRaw: results[string(cluster)]}
		}
	}

	for provider, clusters := range disabledClusters {
		for _, cluster := range clusters {
			config.BuildFarm[provider][cluster] = &dispatcher.BuildFarmConfig{Disabled: true}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}

	if o.createPR {
		if err := o.PRCreationOptions.Finalize(); err != nil {
			logrus.WithError(err).Fatal("Failed to finalize PR creation options")
		}
	}

	if o.PrometheusOptions.PrometheusPasswordPath != "" {
		if err := secret.Add(o.PrometheusOptions.PrometheusPasswordPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	promClient, err := o.PrometheusOptions.NewPrometheusClient(secret.GetSecret)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create prometheus client.")
	}

	v1api := prometheusapi.NewAPI(promClient)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	y, m, d := time.Now().Add(-time.Duration(24*o.prometheusDaysBefore) * time.Hour).Date()
	ts := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	jobVolumes, err := dispatcher.GetJobVolumesFromPrometheus(ctx, v1api, ts)
	logrus.Debugf("we use %s as now to query prometheus", ts.UTC())
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get job volumes from Prometheus.")
	}
	logrus.WithField("jobVolumes", jobVolumes).Debug("loaded job volumes")

	config, err := dispatcher.LoadConfig(o.configPath)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load config from %q", o.configPath)
	}

	if o.defaultCluster != "" {
		config.Default = api.Cluster(o.defaultCluster)
	}

	enabled := o.enableClusters.StringSet()
	disabled := o.disableClusters.StringSet()
	if len(enabled) > 0 || len(disabled) > 0 {
		for provider := range config.BuildFarm {
			for cluster := range config.BuildFarm[provider] {
				if enabled.Has(string(cluster)) {
					config.BuildFarm[provider][cluster].Disabled = false
				}
				if disabled.Has(string(cluster)) {
					config.BuildFarm[provider][cluster].Disabled = true
				}
			}
		}
	}

	logrus.Info("Dispatching ...")
	if err := dispatchJobs(context.TODO(), o.prowJobConfigDir, o.maxConcurrency, config, jobVolumes); err != nil {
		logrus.WithError(err).Fatal("Failed to dispatch")
	}
	if err := dispatcher.SaveConfig(config, o.configPath); err != nil {
		logrus.WithError(err).Fatalf("Failed to save config file to %s", o.configPath)
	}

	if !o.createPR {
		logrus.Info("Finished dispatching and create no PR, exiting ...")
		os.Exit(0)
	}

	logrus.WithField("targetDir", o.targetDir).Info("Changing working directory ...")
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	command := "/usr/bin/sanitize-prow-jobs"
	arguments := []string{"--prow-jobs-dir", "./ci-operator/jobs", "--config-path", "./core-services/sanitize-prow-jobs/_config.yaml"}
	fullCommand := fmt.Sprintf("%s %s", filepath.Base(command), strings.Join(arguments, " "))
	logrus.WithField("fullCommand", fullCommand).Infof("Running the command ...")

	combinedOutput, err := exec.Command(command, arguments...).CombinedOutput()
	if err != nil {
		logrus.WithError(err).WithField("combinedOutput", string(combinedOutput)).Fatal("failed to run the command")
	}

	title := fmt.Sprintf("%s at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := o.PRCreationOptions.UpsertPR(o.targetDir, githubOrg, githubRepo, upstreamBranch, title, prcreation.PrAssignee(o.assign), prcreation.MatchTitle(matchTitle)); err != nil {
		logrus.WithError(err).Fatalf("failed to upsert PR")
	}
}
