package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/logrusutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type options struct {
	prowJobConfigDir string
	configPath       string

	prometheusURL          string
	prometheusUsername     string
	prometheusPasswordPath string
	prometheusDaysBefore   int
	maxConcurrency         int

	prometheusPassword string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	fs.StringVar(&o.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")

	fs.StringVar(&o.prometheusURL, "prometheus-url", "https://prometheus-prow-monitoring.apps.ci.l2s4.p1.openshiftapps.com", "The prometheus URL")
	fs.StringVar(&o.prometheusUsername, "prometheus-username", "", "The Prometheus username.")
	fs.StringVar(&o.prometheusPasswordPath, "prometheus-password-path", "", "The path to a file containing the Prometheus password")
	fs.IntVar(&o.prometheusDaysBefore, "prometheus-days-before", 1, "Number [1,15] of days before. Time 00-00-00 of that day will be used as time to query Prometheus. E.g., 1 means 00-00-00 of yesterday.")
	fs.IntVar(&o.maxConcurrency, "concurrency", 0, "Maximum number of concurrent in-flight goroutines to handle files.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func (o *options) validate() error {
	if o.prowJobConfigDir == "" {
		return fmt.Errorf("mandatory argument --prow-jobs-dir wasn't set")
	}
	if o.configPath == "" {
		return fmt.Errorf("mandatory argument --config-path wasn't set")
	}

	if (o.prometheusUsername == "") != (o.prometheusPasswordPath == "") {
		return fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together")
	}
	if o.prometheusDaysBefore < 1 || o.prometheusDaysBefore > 15 {
		return fmt.Errorf("--prometheus-days-before must be between 1 and 15")
	}
	return nil
}

func (o *options) complete(secrets *sets.String) error {
	if o.prometheusPasswordPath != "" {
		bytes, err := ioutil.ReadFile(o.prometheusPasswordPath)
		if err != nil {
			return err
		}
		o.prometheusPassword = strings.TrimSpace(string(bytes))
		if o.prometheusPassword == "" {
			return fmt.Errorf("no content in file: %s", o.prometheusPasswordPath)
		}
		secrets.Insert(o.prometheusPassword)
	}
	if o.maxConcurrency == 0 {
		o.maxConcurrency = runtime.GOMAXPROCS(0)
	}
	return nil
}

var (
	knownCloudProviders = sets.NewString(string(dispatcher.CloudAWS), string(dispatcher.CloudGCP))
)

// getCloudProviderFromEnv returns the value of environment variable "CLUSTER_TYPE" if defined in the pod's spec; empty string otherwise.
func getCloudProviderFromEnv(spec *corev1.PodSpec) string {
	if spec == nil {
		return ""
	}
	for _, c := range spec.Containers {
		for _, e := range c.Env {
			if e.Name == "CLUSTER_TYPE" {
				if knownCloudProviders.Has(e.Value) {
					return e.Value
				}
			}
		}
	}
	return ""
}

// getCloudProvidersForE2ETests returns a set of cloud providers where a cluster is hosted for an e2e test defined in the given Prow job config.
func getCloudProvidersForE2ETests(jc *prowconfig.JobConfig) sets.String {
	cloudProviders := sets.NewString()
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			if ct := getCloudProviderFromEnv(job.Spec); ct != "" {
				cloudProviders.Insert(ct)
			}
		}
	}
	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			if ct := getCloudProviderFromEnv(job.Spec); ct != "" {
				cloudProviders.Insert(ct)
			}
		}
	}
	for _, job := range jc.Periodics {
		if ct := getCloudProviderFromEnv(job.Spec); ct != "" {
			cloudProviders.Insert(ct)
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
func (cv *clusterVolume) findClusterForJobConfig(cloudProvider string, jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) string {
	//no cluster in the build farm is from the targeting cloud provider
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

	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes)
		}
	}

	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes)
		}
	}
	for _, job := range jc.Periodics {
		cv.addToVolume(rCloudProvider, cluster, job.JobBase, path, config, jobVolumes)
	}

	cv.mutex.Unlock()
	return cluster
}

func (cv *clusterVolume) addToVolume(cloudProvider, cluster string, jobBase prowconfig.JobBase, path string, config *dispatcher.Config, jobVolumes map[string]float64) {
	determinedCluster, canBeRelocated := config.DetermineClusterForJob(jobBase, path)
	if cluster == string(determinedCluster) || canBeRelocated {
		cv.clusterVolumeMap[cloudProvider][cluster] = cv.clusterVolumeMap[cloudProvider][cluster] + jobVolumes[jobBase.Name]
	} else if determinedCloudProvider := config.IsInBuildFarm(determinedCluster); determinedCloudProvider != "" {
		cv.clusterVolumeMap[string(determinedCloudProvider)][string(determinedCluster)] = cv.clusterVolumeMap[string(determinedCloudProvider)][string(determinedCluster)] + jobVolumes[jobBase.Name]
	}
}

// dispatchJobConfig dispatches the jobs defined in a Prow jon config
func (cv *clusterVolume) dispatchJobConfig(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) string {
	cloudProvidersForE2ETests := getCloudProvidersForE2ETests(jc)
	var cluster string
	if cloudProvidersForE2ETests.Len() == 1 {
		cloudProvider, _ := cloudProvidersForE2ETests.PopAny()
		cluster = cv.findClusterForJobConfig(cloudProvider, jc, path, config, jobVolumes)
	} else {
		cluster = cv.findClusterForJobConfig("", jc, path, config, jobVolumes)
	}
	return cluster
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
	// cv stores the volume for each cluster in the build farm
	cv := &clusterVolume{clusterVolumeMap: map[string]map[string]float64{}, cloudProviders: sets.NewString()}
	for cloudProvider, v := range config.BuildFarm {
		cv.cloudProviders.Insert(string(cloudProvider))
		for cluster := range v {
			clusterString := string(cluster)
			cloudProviderString := string(cloudProvider)
			if _, ok := cv.clusterVolumeMap[cloudProviderString]; !ok {
				cv.clusterVolumeMap[cloudProviderString] = map[string]float64{}
			}
			cv.clusterVolumeMap[cloudProviderString][clusterString] = 0
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
					results[o.cluster] = append(results[o.cluster], fmt.Sprintf(".*%s$", o.filename))
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

	if err := filepath.Walk(prowJobConfigDir, func(path string, info os.FileInfo, err error) error {
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

			data, err := ioutil.ReadFile(path)
			if err != nil {
				objChan <- fmt.Errorf("failed to read file %q: %w", path, err)
				return
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				objChan <- fmt.Errorf("failed to unmarshal file %q: %w", path, err)
				return
			}

			cluster := cv.dispatchJobConfig(jobConfig, path, config, jobVolumes)
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
			config.BuildFarm[cloudProvider][cluster] = dispatcher.Group{Paths: results[string(cluster)]}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func main() {
	secrets := sets.NewString()
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, func() sets.String {
		return secrets
	}))
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}
	if err := o.complete(&secrets); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}

	promClient, err := dispatcher.NewPrometheusClient(o.prometheusURL, o.prometheusUsername, o.prometheusPassword)
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
	if err := dispatchJobs(context.TODO(), o.prowJobConfigDir, o.maxConcurrency, config, jobVolumes); err != nil {
		logrus.WithError(err).Fatal("Failed to dispatch")
	}
	if err := dispatcher.SaveConfig(config, o.configPath); err != nil {
		logrus.WithError(err).Fatalf("Failed to save config file to %s", o.configPath)
	}
}
