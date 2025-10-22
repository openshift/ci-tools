package main

import (
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/sanitizer"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	githubOrg      = "openshift"
	githubRepo     = "release"
	githubLogin    = "openshift-bot"
	matchTitle     = "Automate prow job dispatcher"
	upstreamBranch = "main"
	listURL        = "https://github.com/openshift/release/pulls?q=is%3Apr+author%3Aopenshift-bot+prow+job+dispatcher+in%3Atitle+is%3Aopen"
)

type options struct {
	prowJobConfigDir  string
	configPath        string
	clusterConfigPath string
	jobsStoragePath   string

	prometheusDaysBefore int

	upstreamBranch string
	createPR       bool
	githubLogin    string
	targetDir      string
	assign         string

	enableClusters  flagutil.Strings
	disableClusters flagutil.Strings
	defaultCluster  string

	bumper.GitAuthorOptions
	dispatcher.PrometheusOptions
	prcreation.PRCreationOptions

	slackTokenPath string
	opsChannelId   string
}

type slackClient interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	fs.StringVar(&o.configPath, "config-path", "", "Path to the config file (core-services/sanitize-prow-jobs/_config.yaml in openshift/release)")
	fs.StringVar(&o.clusterConfigPath, "cluster-config-path", "core-services/sanitize-prow-jobs/_clusters.yaml", "Path to the config file (core-services/sanitize-prow-jobs/_clusters.yaml in openshift/release)")
	fs.StringVar(&o.jobsStoragePath, "jobs-storage-path", "", "Path to the file holding only job assignments in Gob format")
	fs.IntVar(&o.prometheusDaysBefore, "prometheus-days-before", 14, "Number [1,15] of days before. Time 00-00-00 of that day will be used as time to query Prometheus. E.g., 1 means 00-00-00 of yesterday.")

	fs.BoolVar(&o.createPR, "create-pr", false, "Create a pull request to the change made with this tool.")
	fs.StringVar(&o.upstreamBranch, "upstream-branch", upstreamBranch, "Upstream branch where the PR should be created")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", "ghost", "The github username or group name to assign the created pull request to.")

	fs.Var(&o.enableClusters, "enable-cluster", "Enable this cluster. Does nothing if the cluster is enabled. Can be passed multiple times and must be disjoint with all --disable-cluster values.")
	fs.Var(&o.disableClusters, "disable-cluster", "Disable this cluster. Does nothing if the cluster is disabled. Can be passed multiple times and must be disjoint with all --enable-cluster values.")
	fs.StringVar(&o.defaultCluster, "default-cluster", "", "If passed, changes the default cluster to the specified value.")
	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.StringVar(&o.opsChannelId, "ops-channel-id", "CHY2E1BL4", "Channel ID for #ops-testplatform")

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
	if o.prowJobConfigDir == "" {
		return fmt.Errorf("mandatory argument --prow-jobs-dir wasn't set")
	}
	if o.configPath == "" {
		return fmt.Errorf("mandatory argument --config-path wasn't set")
	}

	if o.prometheusDaysBefore < 1 || o.prometheusDaysBefore > 15 {
		return fmt.Errorf("--prometheus-days-before must be between 1 and 15")
	}

	if o.clusterConfigPath == "" {
		logrus.Fatal("mandatory argument --cluster-config-path wasn't set")
	}

	if o.jobsStoragePath == "" {
		logrus.Fatal("mandatory argument --jobs-storage-path wasn't set")
	}

	if o.slackTokenPath == "" {
		logrus.Fatal("mandatory argument --slack-token-path wasn't set")
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
func getCloudProvidersForE2ETests(jc *prowconfig.JobConfig) sets.Set[string] {
	cloudProviders := sets.New[string]()
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
	specialClusters  map[string]float64
	// only needed for stable tests: traverse the above map by sorted key list
	cloudProviders     sets.Set[string]
	pjs                map[string]dispatcher.ProwJobData
	blocked            sets.Set[string]
	volumeDistribution map[string]float64
	clusterMap         dispatcher.ClusterMap
}

// findClusterForJobConfig finds a cluster running on a preferred cloud provider for the jobs in a Prow job config.
// The chosen cluster will be the one with minimal workload with the given cloud provider.
// If the cluster provider is empty string, it will choose the one with minimal workload across all cloud providers.
func (cv *clusterVolume) findClusterForJobConfig(cloudProvider string, jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) (string, error) {
	if _, ok := cv.clusterVolumeMap[cloudProvider]; !ok {
		cloudProvider = ""
	}
	var cluster string
	var totalVolume float64
	for _, volume := range jobVolumes {
		totalVolume += volume
	}

	mostUsedCluster := dispatcher.FindMostUsedCluster(jc)
	// TODO: 75% as we still have manual assignments and these are affecting even distribution, re-evaluate when manual assignments are gone
	if determinedCloudProvider := config.IsInBuildFarm(api.Cluster(mostUsedCluster)); determinedCloudProvider != "" &&
		cv.clusterVolumeMap[string(determinedCloudProvider)][mostUsedCluster] < cv.volumeDistribution[mostUsedCluster]*0.75 {
		cluster = mostUsedCluster
	} else {
		min := float64(-1)
		for _, cp := range sets.List(cv.cloudProviders) {
			m := cv.clusterVolumeMap[cp]
			for c, v := range m {
				if cv.clusterMap[c].Capacity != 100 {
					continue
				}
				if cloudProvider == "" || cloudProvider == cp {
					if min < 0 || min > v {
						min = v
						cluster = c
					}
				}
			}
		}
	}

	var errs []error
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			if err := cv.addToVolume(cluster, job.JobBase, path, config, jobVolumes); err != nil {
				errs = append(errs, err)
			}
		}
	}

	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			if err := cv.addToVolume(cluster, job.JobBase, path, config, jobVolumes); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, job := range jc.Periodics {
		if err := cv.addToVolume(cluster, job.JobBase, path, config, jobVolumes); err != nil {
			errs = append(errs, err)
		}
	}

	return cluster, utilerrors.NewAggregate(errs)
}

func extractCapabilities(labels map[string]string) []string {
	var capabilities []string
	prefix := "capability/"

	for key, value := range labels {
		if strings.HasPrefix(key, prefix) {
			capabilities = append(capabilities, value)
		}
	}

	return capabilities
}

func findClusterAssigmentsForJobs(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, pjs map[string]dispatcher.ProwJobData, blocked sets.Set[string], cm dispatcher.ClusterMap) error {
	mostUsedCluster := dispatcher.FindMostUsedCluster(jc)

	getClusterForMissingJob := func(cluster string, jobBase prowconfig.JobBase, pjs map[string]dispatcher.ProwJobData) error {
		determinedCluster, canBeRelocated, err := config.DetermineClusterForJob(jobBase, path, cm)
		if err != nil {
			return fmt.Errorf("failed to determine cluster for the job %s in path %q: %w", jobBase.Name, path, err)
		}

		c := dispatcher.DetermineTargetCluster(cluster, string(determinedCluster), string(config.Default), canBeRelocated, blocked)
		pjs[jobBase.Name] = dispatcher.ProwJobData{Cluster: c, Capabilities: extractCapabilities(jobBase.Labels)}
		logrus.WithField("job", jobBase.Name).WithField("cluster", c).Info("found cluster for job")
		return nil
	}

	var errs []error
	for k := range jc.PresubmitsStatic {
		for _, job := range jc.PresubmitsStatic[k] {
			if _, ok := pjs[job.Name]; !ok || !slices.Equal(pjs[job.Name].Capabilities, extractCapabilities(job.Labels)) {
				if err := getClusterForMissingJob(mostUsedCluster, job.JobBase, pjs); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}

	for k := range jc.PostsubmitsStatic {
		for _, job := range jc.PostsubmitsStatic[k] {
			if _, ok := pjs[job.Name]; !ok || !slices.Equal(pjs[job.Name].Capabilities, extractCapabilities(job.Labels)) {
				if err := getClusterForMissingJob(mostUsedCluster, job.JobBase, pjs); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	for _, job := range jc.Periodics {
		if _, ok := pjs[job.Name]; !ok || !slices.Equal(pjs[job.Name].Capabilities, extractCapabilities(job.Labels)) {
			if err := getClusterForMissingJob(mostUsedCluster, job.JobBase, pjs); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (cv *clusterVolume) addToVolume(cluster string, jobBase prowconfig.JobBase, path string, config *dispatcher.Config, jobVolumes map[string]float64) error {
	determinedCluster, canBeRelocated, err := config.DetermineClusterForJob(jobBase, path, cv.clusterMap)

	if err != nil {
		return fmt.Errorf("failed to determine cluster for the job %s in path %q: %w", jobBase.Name, path, err)
	}

	c := dispatcher.DetermineTargetCluster(cluster, string(determinedCluster), string(config.Default), canBeRelocated, cv.blocked)
	cv.pjs[jobBase.Name] = dispatcher.ProwJobData{Cluster: c, Capabilities: extractCapabilities(jobBase.Labels)}
	if determinedCloudProvider := config.IsInBuildFarm(api.Cluster(c)); determinedCloudProvider != "" {
		cv.clusterVolumeMap[string(determinedCloudProvider)][c] = cv.clusterVolumeMap[string(determinedCloudProvider)][c] + jobVolumes[jobBase.Name]
		return nil
	}
	cv.specialClusters[c] = cv.specialClusters[c] + jobVolumes[jobBase.Name]
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

type fileSizeInfo struct {
	path string
	info fs.DirEntry
	size int64
}

// dispatchJobs loads the Prow jobs and chooses a cluster in the build farm if possible.
// The current implementation walks through the Prow Job config files.
// For each file, it tries to assign all jobs in it to a cluster in the build farm.
//   - When all the e2e tests are targeting the same cloud provider, we run the test pod on the that cloud provider too.
//   - When the e2e tests are targeting different cloud providers, or there is no e2e tests at all, we can run the tests
//     on any cluster in the build farm. Those jobs are used to load balance the workload of clusters in the build farm.
func dispatchJobs(prowJobConfigDir string, config *dispatcher.Config, jobVolumes map[string]float64, blocked sets.Set[string], volumeDistribution map[string]float64, cm dispatcher.ClusterMap) (map[string]dispatcher.ProwJobData, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// cv stores the volume for each cluster in the build farm
	cv := &clusterVolume{
		clusterVolumeMap:   map[string]map[string]float64{},
		cloudProviders:     sets.New[string](),
		pjs:                map[string]dispatcher.ProwJobData{},
		blocked:            blocked,
		specialClusters:    map[string]float64{},
		volumeDistribution: volumeDistribution,
		clusterMap:         cm}
	for cloudProvider, v := range config.BuildFarm {
		for cluster := range v {
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
		return nil, nil
	}

	results := map[string][]string{}
	var errs []error

	dispatch := func(jobConfig *prowconfig.JobConfig, path string, info fs.DirEntry) {
		cluster, err := cv.dispatchJobConfig(jobConfig, path, config, jobVolumes)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to dispatch job config %q: %w", path, err))

		}
		cr := configResult{cluster: cluster, path: path, filename: info.Name()}
		if !config.MatchingPathRegEx(cr.path) {
			results[cr.cluster] = append(results[cr.cluster], cr.filename)
		}
	}
	fileList, err := composeFileInfoList(prowJobConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch all Prow jobs: %w", err)
	}

	sort.Slice(fileList, func(i, j int) bool { return fileList[i].size > fileList[j].size })
	if err := dispatchEveryFile(fileList, dispatch); err != nil {
		errs = append(errs, err)
	}

	for cloudProvider, m := range cv.clusterVolumeMap {
		for cluster, volume := range m {
			logrus.WithField("cloudProvider", cloudProvider).WithField("cluster", cluster).WithField("volume", volume).Info("dispatched the volume on the cluster")
		}
	}

	for cluster, volume := range cv.specialClusters {
		logrus.WithField("cluster", cluster).WithField("volume", volume).Info("dispatched the volume on the cluster")
	}
	for cloudProvider, jobGroups := range config.BuildFarm {
		for cluster := range jobGroups {
			config.BuildFarm[cloudProvider][cluster] = &dispatcher.BuildFarmConfig{FilenamesRaw: results[string(cluster)]}
		}
	}

	return cv.pjs, utilerrors.NewAggregate(errs)
}

func dispatchDeltaJobs(prowJobConfigDir string, config *dispatcher.Config, blocked sets.Set[string], pjs map[string]dispatcher.ProwJobData, cm dispatcher.ClusterMap) error {
	var errs []error
	dispatch := func(jobConfig *prowconfig.JobConfig, path string, info fs.DirEntry) {
		if err := findClusterAssigmentsForJobs(jobConfig, path, config, pjs, blocked, cm); err != nil {
			errs = append(errs, err)
		}
	}
	fileList, err := composeFileInfoList(prowJobConfigDir)
	if err != nil {
		return fmt.Errorf("failed to dispatch all Prow jobs: %w", err)
	}

	sort.Slice(fileList, func(i, j int) bool { return fileList[i].size > fileList[j].size })
	if err := dispatchEveryFile(fileList, dispatch); err != nil {
		errs = append(errs, err)
	}
	return utilerrors.NewAggregate(errs)
}

func dispatchEveryFile(fileList []fileSizeInfo, dispatch func(jobConfig *prowconfig.JobConfig, path string, info fs.DirEntry)) error {
	var errs []error
	for _, file := range fileList {
		func(path string, info fs.DirEntry) {
			data, err := gzip.ReadFileMaybeGZIP(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to read file %q: %w", path, err))
				return
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				errs = append(errs, fmt.Errorf("failed to unmarshal file %q: %w", path, err))

				return
			}
			dispatch(jobConfig, path, info)

		}(file.path, file.info)
	}
	return utilerrors.NewAggregate(errs)
}

func composeFileInfoList(prowJobConfigDir string) ([]fileSizeInfo, error) {
	fileList := make([]fileSizeInfo, 0)
	var errs []error
	if err := filepath.WalkDir(prowJobConfigDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to walk file/directory '%s'", path))
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		fileInfo, err := os.Stat(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get file info for '%s': %w", path, err))
			return nil
		}

		fileList = append(fileList, fileSizeInfo{
			path: path,
			info: info,
			size: fileInfo.Size(),
		})
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	return fileList, utilerrors.NewAggregate(errs)
}

// removeDisabledClusters removes disabled clusters from BuildFarm and BuildFarmConfig
func removeDisabledClusters(config *dispatcher.Config, disabled sets.Set[string]) {
	for provider := range config.BuildFarm {
		for cluster := range config.BuildFarm[provider] {
			if disabled.Has(string(cluster)) {
				delete(config.BuildFarm[provider], cluster)
				if clusters, ok := config.BuildFarmCloud[provider]; ok {
					c := sets.New[string](clusters...)
					c = c.Delete(string(cluster))
					config.BuildFarmCloud[provider] = sets.List(c)
				}
			}
		}
	}
}

type clusterProviderGetter func(cluster string) (api.Cloud, error)

// addEnabledClusters adds enabled clusters to the BuildFarm and BuildFarmConfig
func addEnabledClusters(config *dispatcher.Config, enabled sets.Set[string], getter clusterProviderGetter) {
	if len(enabled) > 0 && config.BuildFarm == nil {
		config.BuildFarm = make(map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig)
	}
	for cluster := range enabled {
		provider, err := getter(cluster)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to get cluster cloud provider information")
		}
		if _, exists := config.BuildFarm[provider][api.Cluster(cluster)]; !exists {
			if config.BuildFarm[provider] == nil {
				config.BuildFarm[provider] = make(map[api.Cluster]*dispatcher.BuildFarmConfig)
			}
			config.BuildFarm[provider][api.Cluster(cluster)] = &dispatcher.BuildFarmConfig{FilenamesRaw: []string{}, Filenames: sets.New[string]()}
		}
		if clusters, ok := config.BuildFarmCloud[provider]; ok {
			clusters = append(clusters, cluster)
			config.BuildFarmCloud[provider] = clusters
		} else {
			if config.BuildFarmCloud == nil {
				config.BuildFarmCloud = make(map[api.Cloud][]string)
			}
			config.BuildFarmCloud[provider] = []string{cluster}
		}
	}
}

func getEnabledClusters(config *dispatcher.Config) sets.Set[string] {
	enabled := sets.New[string]()
	for _, clusters := range config.BuildFarm {
		for cluster := range clusters {
			enabled.Insert(string(cluster))
		}
	}
	return enabled
}

func getDiffClusters(enabledClusters, clustersFromConfig sets.Set[string]) (clustersToAdd, clustersToRemove sets.Set[string]) {
	return clustersFromConfig.Difference(enabledClusters), enabledClusters.Difference(clustersFromConfig)
}

func clustersMapToSet(clusterMap dispatcher.ClusterMap) sets.Set[string] {
	clusterSet := sets.Set[string]{}
	for cluster := range clusterMap {
		clusterSet.Insert(cluster)
	}
	return clusterSet
}

func gitCloneRelease() error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--single-branch", "https://github.com/openshift/release.git")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w, output: %s", err, string(output))
	}

	return nil
}

func cleanup(directory string) {
	err := os.RemoveAll(directory)
	if err != nil {
		logrus.WithField("directory", directory).WithError(err).Error("failed to remove directory")
	}
	logrus.WithField("directory", directory).Info("Successfully removed directory")
}

// createPR creates PR with config changes and sanitizer changes, it causes app to exit in
// case of failure to trigger re-run of logic
func createPR(o options, config *dispatcher.Config, pjs map[string]dispatcher.ProwJobData, cm dispatcher.ClusterMap) {
	targetDirWithRelease := filepath.Join(o.targetDir, "/release")
	cleanup(targetDirWithRelease)
	defer cleanup(targetDirWithRelease)

	logrus.WithField("targetDir", o.targetDir).Info("Changing working directory ...")
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("failed to change to root dir")
	}

	if err := gitCloneRelease(); err != nil {
		logrus.WithError(err).Fatal("failed to clone release repository")
	}

	if err := dispatcher.SaveConfig(config, filepath.Join(targetDirWithRelease, "/core-services/sanitize-prow-jobs/_config.yaml")); err != nil {
		logrus.WithError(err).WithField("configPath", o.configPath).Fatal("failed to save config file")
	}

	if err := sanitizer.DeterminizeJobs(filepath.Join(targetDirWithRelease, "/ci-operator/jobs"), config, pjs, make(sets.Set[string]), cm); err != nil {
		logrus.WithError(err).Fatal("failed to determinize")
	}

	title := fmt.Sprintf("%s at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := o.PRCreationOptions.UpsertPR(targetDirWithRelease, githubOrg, githubRepo, o.upstreamBranch, title, prcreation.PrAssignee(o.assign), prcreation.MatchTitle(matchTitle), prcreation.AdditionalLabels([]string{rehearse.RehearsalsAckLabel})); err != nil {
		logrus.WithError(err).Fatal("failed to upsert PR")
	}
}

func sendSlackMessage(slackClient slackClient, channelId string) error {
	blockMessage := slack.MsgOptionBlocks(
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Scheduling PR to merge*\n\n<@dptp-triage> Prow jobs have been rescheduled. To ensure the proper functioning of legacy tooling, please prioritize merging PRs from this *<%s|list>*.", listURL), false, false),
			nil,
			nil,
		),
	)
	_, _, err := slackClient.PostMessage(channelId, blockMessage)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	logrusutil.Init(
		&logrusutil.DefaultFieldsFormatter{
			PrintLineNumber: true,
			DefaultFields:   logrus.Fields{"component": "prow-job-dispatcher"},
		},
	)
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}

	if o.createPR {
		if err := o.PRCreationOptions.Finalize(); err != nil {
			logrus.WithError(err).Fatal("Failed to finalize PR creation options")
		}
	}

	if o.PrometheusPasswordPath != "" {
		if err := secret.Add(o.PrometheusPasswordPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	if o.PrometheusBearerTokenPath != "" {
		if err := secret.Add(o.PrometheusBearerTokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	promVolumes, err := dispatcher.NewPrometheusVolumes(o.PrometheusOptions, o.prometheusDaysBefore)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prometheus volumes")
	}

	if err := secret.Add(o.slackTokenPath); err != nil {
		logrus.WithError(err).Fatal("failed to start secrets agent")
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		logrus.Info("Ctrl+C pressed. Exiting immediately.")
		os.Exit(0)
	}()

	var dispatchWrapper func(forceDispatch bool)
	var dispatchDeltaWrapper func()
	prowjobs := dispatcher.NewProwjobs(o.jobsStoragePath)
	c := cron.New()

	// Pass an empty cluster list to it. This works as long as it's guaranteed that the
	// dispatchWrapper func below is called one when this program starts.
	ecd := dispatcher.NewEphemeralClusterDispatcher([]string{})

	{
		var mu sync.Mutex
		slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))

		dispatchDeltaWrapper = func() {
			mu.Lock()
			defer mu.Unlock()
			config, err := dispatcher.LoadConfig(o.configPath)
			if err != nil {
				logrus.WithError(err).Errorf("failed to load config from %q", o.configPath)
				return
			}
			cm, blocked, err := dispatcher.LoadClusterConfig(o.clusterConfigPath)
			if err != nil {
				logrus.WithError(err).Error("failed to load cluster config")
				return
			}

			pjs := prowjobs.GetDataCopy()

			if err := dispatchDeltaJobs(o.prowJobConfigDir, config, blocked, pjs, cm); err != nil {
				logrus.WithError(err).Error("failed to dispatch")
				return
			}
			prowjobs.Regenerate(pjs)
		}

		dispatchWrapper = func(forceDispatch bool) {
			mu.Lock()
			defer mu.Unlock()

			config, err := dispatcher.LoadConfig(o.configPath)
			if err != nil {
				logrus.WithError(err).Errorf("failed to load config from %q", o.configPath)
				return
			}

			configClusterMap, blocked, err := dispatcher.LoadClusterConfig(o.clusterConfigPath)
			if err != nil {
				logrus.WithError(err).Error("failed to load cluster config")
				return
			}
			clustersFromConfig := clustersMapToSet(configClusterMap)

			enabled, disabled := getDiffClusters(getEnabledClusters(config), clustersFromConfig)
			if len(disabled) > 0 {
				removeDisabledClusters(config, disabled)
			}

			newBlockedClusters := prowjobs.HasAnyOfClusters(blocked)

			if (!forceDispatch && enabled.Len() == 0 && disabled.Len() == 0) && !newBlockedClusters {
				return
			}

			jobVolumes, err := promVolumes.GetJobVolumes()
			if err != nil {
				logrus.WithError(err).Fatal("failed to get job volumes")
			}

			addEnabledClusters(config, enabled,
				func(cluster string) (api.Cloud, error) {
					info, exists := configClusterMap[cluster]
					if !exists {
						return "", fmt.Errorf("have not found provider for cluster %s", cluster)
					}
					return api.Cloud(info.Provider), nil
				})
			pjs, err := dispatchJobs(o.prowJobConfigDir, config, jobVolumes, blocked, promVolumes.CalculateVolumeDistribution(configClusterMap), configClusterMap)
			if err != nil {
				logrus.WithError(err).Error("failed to dispatch")
				return
			}
			prowjobs.Regenerate(pjs)

			ecd.Reset(clustersFromConfig.UnsortedList())

			if err := dispatcher.WriteGob(o.jobsStoragePath, pjs); err != nil {
				logrus.WithError(err).Errorf("continuing on cache memory, error writing Gob file")
			}

			if o.createPR {
				createPR(o, config, pjs, configClusterMap)
				if err := sendSlackMessage(slackClient, o.opsChannelId); err != nil {
					logrus.WithError(err).Error("Failed to post message in ops channel")
				}
			}
		}
	}

	cronDispatchWrapper := func() {
		dispatchWrapper(true)
	}

	_, err = c.AddFunc("0 7 * * 0", cronDispatchWrapper)
	if err != nil {
		logrus.WithError(err).Fatal("error scheduling cron job")
	}
	c.Start()

	// In the long term, git-sync and shallow syncing can affect the modification time,
	// making it inconsistent with the actual data in the repository. To address this,
	// the cluster config data is loaded every minute and checked for changes.
	go func(config string) {
		// Ticker for checking the cluster config every minute
		configTicker := time.NewTicker(time.Minute)
		defer configTicker.Stop()

		deltaTicker := time.NewTicker(5 * time.Minute)
		defer deltaTicker.Stop()

		prevConfigClusterMap, prevBlocked, err := dispatcher.LoadClusterConfig(config)
		if err != nil {
			logrus.WithError(err).Fatal("failed to load initial cluster config")
			return
		}
		// Run dispatch for the first time
		dispatchWrapper(false)

		for {
			select {
			case <-configTicker.C:
				currentConfigClusterMap, currentBlocked, err := dispatcher.LoadClusterConfig(config)
				if err != nil {
					logrus.WithError(err).Error("failed to load cluster config")
					continue
				}

				if !reflect.DeepEqual(currentConfigClusterMap, prevConfigClusterMap) || !reflect.DeepEqual(currentBlocked, prevBlocked) {
					logrus.WithField("prevConfigClusterMap", prevConfigClusterMap).WithField("prevBlocked", prevBlocked).
						WithField("currentConfigClusterMap", currentConfigClusterMap).WithField("currentBlocked", currentBlocked).Info("new dispatch")
					dispatchWrapper(dispatcher.HasCapacityOrCapabilitiesChanged(prevConfigClusterMap, currentConfigClusterMap))
					prevConfigClusterMap = currentConfigClusterMap
					prevBlocked = currentBlocked
				}

			case <-deltaTicker.C:
				dispatchDeltaWrapper()
			}
		}
	}(o.clusterConfigPath)

	server := dispatcher.NewServer(prowjobs, ecd, dispatchWrapper)
	http.HandleFunc("/", server.RequestHandler)
	http.HandleFunc("/event", server.EventHandler)
	logrus.Fatal(http.ListenAndServe(":8080", nil))
}
