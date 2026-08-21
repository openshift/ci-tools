package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/sanitizer"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	githubOrg              = "openshift"
	githubRepo             = "release"
	githubLogin            = "openshift-bot"
	matchTitle             = "Automate prow job dispatcher"
	upstreamBranch         = "master"
	listURL                = "https://github.com/openshift/release/pulls?q=is%3Apr+author%3Aopenshift-bot+prow+job+dispatcher+in%3Atitle+is%3Aopen"
	inventoryDigestVersion = 1
)

var blockedClusterRelocationJobExceptions = []*regexp.Regexp{
	regexp.MustCompile(`^periodic-build[0-9]{2}-upgrade$`),
}

type options struct {
	prowJobConfigDir    string
	configPath          string
	clusterConfigPath   string
	jobsStoragePath     string
	snapshotStoragePath string

	prometheusDaysBefore       int
	rebalanceHysteresisPercent float64
	movementBudgetPercent      float64

	upstreamBranch string
	createPR       bool
	githubLogin    string
	targetDir      string
	assign         string
	prBody         string

	enableClusters  flagutil.Strings
	disableClusters flagutil.Strings
	defaultCluster  string

	bumper.GitAuthorOptions
	dispatcher.PrometheusOptions
	prcreation.PRCreationOptions

	slackTokenPath string
	opsChannelId   string

	overrideNamespace               string
	controlAPITokenPath             string
	fallbackObserverTokenPath       string
	dispatchCommandChannelID        string
	enableRuntimeCapacity           bool
	enableRuntimeDrain              bool
	enableRuntimeCapabilityScope    bool
	maxOverrideTTL                  time.Duration
	maxDrainTTL                     time.Duration
	planTTL                         time.Duration
	affectedDemandApprovalThreshold float64
	schedulerPropagationBound       time.Duration
	prowSchedulerConfigPath         string
	legacyEventEndpoint             bool
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
	fs.StringVar(&o.snapshotStoragePath, "snapshot-storage-path", "", "Path to the atomic versioned policy snapshot cache. Defaults beside --jobs-storage-path.")
	fs.IntVar(&o.prometheusDaysBefore, "prometheus-days-before", 0, "Number [0,15] of days before the current time at which the seven-day demand query is evaluated. Zero uses the most recent seven days.")
	fs.Float64Var(&o.rebalanceHysteresisPercent, "rebalance-hysteresis-percent", 10, "Keep an eligible prior baseline placement when its normalized score is within this percentage of the best candidate.")
	fs.Float64Var(&o.movementBudgetPercent, "movement-budget-percent", 10, "Maximum percentage of observed demand moved by one routine baseline generation. Forced moves from blocked or ineligible clusters are exempt.")

	fs.BoolVar(&o.createPR, "create-pr", false, "Create a pull request to the change made with this tool.")
	fs.StringVar(&o.upstreamBranch, "upstream-branch", upstreamBranch, "Upstream branch where the PR should be created")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", "ghost", "The github username or group name to assign the created pull request to.")
	fs.StringVar(&o.prBody, "pr-body", "", "Body text for created or updated pull requests. Can be used to disable bots add carbon copy, etc.")

	fs.Var(&o.enableClusters, "enable-cluster", "Enable this cluster. Does nothing if the cluster is enabled. Can be passed multiple times and must be disjoint with all --disable-cluster values.")
	fs.Var(&o.disableClusters, "disable-cluster", "Disable this cluster. Does nothing if the cluster is disabled. Can be passed multiple times and must be disjoint with all --enable-cluster values.")
	fs.StringVar(&o.defaultCluster, "default-cluster", "", "If passed, changes the default cluster to the specified value.")
	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.StringVar(&o.opsChannelId, "ops-channel-id", "CHY2E1BL4", "Channel ID for #ops-testplatform")
	fs.StringVar(&o.overrideNamespace, "dispatch-override-namespace", "", "Namespace containing DispatchOverride resources. Empty disables the runtime control plane.")
	fs.StringVar(&o.controlAPITokenPath, "control-api-token-path", "", "Path to the bearer token accepted from the DPTP bot control client.")
	fs.StringVar(&o.fallbackObserverTokenPath, "fallback-observer-token-path", "", "Path to the separate bearer token accepted from the Prow fallback-state observer.")
	fs.StringVar(&o.dispatchCommandChannelID, "dispatch-command-channel-id", "", "Immutable Slack channel ID for #team-dp-testplatform.")
	fs.BoolVar(&o.enableRuntimeCapacity, "enable-runtime-capacity", false, "Allow whole-cluster capacity overrides to be applied.")
	fs.BoolVar(&o.enableRuntimeDrain, "enable-runtime-drain", false, "Allow whole-cluster drains to be applied.")
	fs.BoolVar(&o.enableRuntimeCapabilityScope, "enable-runtime-capability-scope", false, "Allow capability-scoped runtime controls.")
	fs.DurationVar(&o.maxOverrideTTL, "max-runtime-override-ttl", 24*time.Hour, "Maximum TTL for a runtime capacity override.")
	fs.DurationVar(&o.maxDrainTTL, "max-runtime-drain-ttl", 2*time.Hour, "Maximum TTL for a runtime drain.")
	fs.DurationVar(&o.planTTL, "runtime-plan-ttl", 15*time.Minute, "How long an unapplied runtime plan remains valid.")
	fs.Float64Var(&o.affectedDemandApprovalThreshold, "runtime-second-approval-demand", 0, "Affected demand at or above this value requires a distinct second approver. A positive value is required before writes can be enabled.")
	fs.DurationVar(&o.schedulerPropagationBound, "scheduler-propagation-bound", 0, "Measured upper bound from dispatcher publication through the Prow external-scheduler cache. Required and at most 30s before writes can be enabled.")
	fs.StringVar(&o.prowSchedulerConfigPath, "prow-scheduler-config-path", "", "Path to the live Prow configuration mounted from the same source used by the scheduler. Required before runtime writes can be enabled.")
	fs.BoolVar(&o.legacyEventEndpoint, "enable-legacy-event-endpoint", false, "Expose the unauthenticated legacy /event dispatch endpoint. Disabled for the next-generation control plane.")

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

	if o.prometheusDaysBefore < 0 || o.prometheusDaysBefore > 15 {
		return fmt.Errorf("--prometheus-days-before must be between 0 and 15")
	}
	if o.rebalanceHysteresisPercent < 0 || o.rebalanceHysteresisPercent > 100 {
		return fmt.Errorf("--rebalance-hysteresis-percent must be between 0 and 100")
	}
	if o.movementBudgetPercent < 0 || o.movementBudgetPercent > 100 {
		return fmt.Errorf("--movement-budget-percent must be between 0 and 100")
	}

	if o.clusterConfigPath == "" {
		logrus.Fatal("mandatory argument --cluster-config-path wasn't set")
	}

	if o.jobsStoragePath == "" {
		logrus.Fatal("mandatory argument --jobs-storage-path wasn't set")
	}
	if o.snapshotStoragePath == "" {
		o.snapshotStoragePath = o.jobsStoragePath + ".snapshot.json"
	}

	if o.slackTokenPath == "" {
		logrus.Fatal("mandatory argument --slack-token-path wasn't set")
	}
	controlConfigured := o.overrideNamespace != "" || o.controlAPITokenPath != "" || o.fallbackObserverTokenPath != "" || o.dispatchCommandChannelID != "" ||
		o.enableRuntimeCapacity || o.enableRuntimeDrain || o.enableRuntimeCapabilityScope
	if controlConfigured && (o.overrideNamespace == "" || o.controlAPITokenPath == "" || o.dispatchCommandChannelID == "") {
		return fmt.Errorf("--dispatch-override-namespace, --control-api-token-path, and --dispatch-command-channel-id are all required for runtime controls")
	}
	if o.enableRuntimeDrain && !o.enableRuntimeCapacity {
		return fmt.Errorf("--enable-runtime-drain requires --enable-runtime-capacity")
	}
	if o.enableRuntimeDrain && o.fallbackObserverTokenPath == "" {
		return fmt.Errorf("--enable-runtime-drain requires --fallback-observer-token-path")
	}
	if o.fallbackObserverTokenPath != "" && o.fallbackObserverTokenPath == o.controlAPITokenPath {
		return fmt.Errorf("--fallback-observer-token-path must use a token distinct from --control-api-token-path")
	}
	if o.enableRuntimeCapabilityScope && !o.enableRuntimeCapacity {
		return fmt.Errorf("--enable-runtime-capability-scope requires --enable-runtime-capacity")
	}
	controlOptions := dispatcher.ControlOptions{
		AllowedChannelID: o.dispatchCommandChannelID, MaxTTL: o.maxOverrideTTL, MaxDrainTTL: o.maxDrainTTL,
		PlanTTL: o.planTTL, AffectedDemandApproval: o.affectedDemandApprovalThreshold,
		EnableCapacity: o.enableRuntimeCapacity, EnableDrain: o.enableRuntimeDrain, EnableCapabilityScope: o.enableRuntimeCapabilityScope,
		SchedulerPropagationBound: o.schedulerPropagationBound,
		WriteSafetyCheck:          o.schedulerWriteSafetyCheck(),
	}
	if controlConfigured {
		if err := controlOptions.Validate(); err != nil {
			return fmt.Errorf("invalid runtime control options: %w", err)
		}
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

func (o *options) schedulerWriteSafetyCheck() func() error {
	if !o.enableRuntimeCapacity {
		return nil
	}
	return func() error {
		return verifyProwSchedulerCacheBound(o.prowSchedulerConfigPath, o.schedulerPropagationBound)
	}
}

func verifyProwSchedulerCacheBound(path string, propagationBound time.Duration) error {
	if path == "" {
		return errors.New("--prow-scheduler-config-path is required before runtime writes can be enabled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Prow scheduler configuration: %w", err)
	}
	var config struct {
		Scheduler prowconfig.Scheduler `json:"scheduler"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse Prow scheduler configuration: %w", err)
	}
	if !config.Scheduler.Enabled || config.Scheduler.External == nil {
		return errors.New("Prow external scheduling is not enabled")
	}
	entryTimeout := config.Scheduler.External.Cache.EntryTimeoutInterval
	if entryTimeout == nil || entryTimeout.Duration <= 0 {
		return errors.New("Prow external scheduler cache entry_timeout_interval must be positive")
	}
	if entryTimeout.Duration > propagationBound {
		return fmt.Errorf("Prow external scheduler cache entry_timeout_interval %s exceeds propagation bound %s", entryTimeout.Duration, propagationBound)
	}
	return nil
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
	cloudProviders      sets.Set[string]
	pjs                 map[string]dispatcher.ProwJobData
	blocked             sets.Set[string]
	volumeDistribution  map[string]float64
	clusterMap          dispatcher.ClusterMap
	prowJobConfigDir    string
	previousAssignments map[string]dispatcher.ProwJobData
	hysteresis          float64
	movementBudget      float64
	movedDemand         float64
	minimumJobDemand    float64
}

func sortedStringKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func previousClusterForProjectedJobs(jobs []projectedJob) string {
	counts := make(map[string]int)
	for _, job := range jobs {
		if job.canBeRelocated && job.previousCluster != "" {
			counts[job.previousCluster]++
		}
	}
	selected, selectedCount := "", 0
	for cluster, count := range counts {
		if count > selectedCount || count == selectedCount && (selected == "" || cluster < selected) {
			selected, selectedCount = cluster, count
		}
	}
	return selected
}

type projectedJob struct {
	determinedCluster string
	canBeRelocated    bool
	blocked           sets.Set[string]
	volume            float64
	previousCluster   string
}

func demandForJob(jobVolumes map[string]float64, jobName string, minimumJobDemand float64) float64 {
	if demand := jobVolumes[jobName]; demand > 0 {
		return demand
	}
	if minimumJobDemand > 0 {
		return minimumJobDemand
	}
	return 1
}

// findClusterForJobConfig finds a cluster running on a preferred cloud provider for the jobs in a Prow job config.
// The chosen cluster will be the one with minimal workload with the given cloud provider.
// If the cluster provider is empty string, it will choose the one with minimal workload across all cloud providers.
func (cv *clusterVolume) findClusterForJobConfig(cloudProvider string, jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) (string, error) {
	if _, ok := cv.clusterVolumeMap[cloudProvider]; !ok {
		cloudProvider = ""
	}
	var cluster string
	projectedVolumes := make(map[string]float64)
	var jobsForProjection []projectedJob
	var jobsForProjectionErr error
	jobsClassified := false
	projectedVolume := func(candidate string) (float64, error) {
		if volume, exists := projectedVolumes[candidate]; exists {
			return volume, nil
		}
		if !jobsClassified {
			jobsClassified = true
			jobsForProjection, jobsForProjectionErr = cv.classifyJobsForProjection(jc, path, config, jobVolumes)
		}
		if jobsForProjectionErr != nil {
			return 0, jobsForProjectionErr
		}
		volume := projectedVolumeForCandidate(candidate, string(config.Default), jobsForProjection)
		projectedVolumes[candidate] = volume
		return volume, nil
	}

	mostUsedCluster := dispatcher.FindMostUsedCluster(jc)
	// TODO: 75% as we still have manual assignments and these are affecting even distribution, re-evaluate when manual assignments are gone
	if determinedCloudProvider := config.IsInBuildFarm(api.Cluster(mostUsedCluster)); determinedCloudProvider != "" &&
		(cloudProvider == "" || cloudProvider == string(determinedCloudProvider)) {
		volume, err := projectedVolume(mostUsedCluster)
		if err != nil {
			return "", err
		}
		if cv.clusterVolumeMap[string(determinedCloudProvider)][mostUsedCluster]+volume < cv.volumeDistribution[mostUsedCluster]*0.75 {
			cluster = mostUsedCluster
		}
	}
	if cluster == "" {
		tieBreakPath, err := repositoryRelativeTieBreakPath(cv.prowJobConfigDir, path)
		if err != nil {
			return "", err
		}
		minScore := float64(-1)
		var minTieBreak uint64
		for _, cp := range sets.List(cv.cloudProviders) {
			m := cv.clusterVolumeMap[cp]
			clusters := make([]string, 0, len(m))
			for candidate := range m {
				clusters = append(clusters, candidate)
			}
			sort.Strings(clusters)
			for _, candidate := range clusters {
				clusterInfo, exists := cv.clusterMap[candidate]
				if !exists || clusterInfo.Capacity <= 0 {
					continue
				}
				if cloudProvider == "" || cloudProvider == cp {
					candidateVolume, err := projectedVolume(candidate)
					if err != nil {
						return "", err
					}
					score := (m[candidate] + candidateVolume) / float64(clusterInfo.Capacity)
					tieBreak := stableClusterTieBreak(tieBreakPath, candidate)
					if minScore < 0 || score < minScore ||
						score == minScore && (tieBreak < minTieBreak || tieBreak == minTieBreak && candidate < cluster) {
						minScore = score
						minTieBreak = tieBreak
						cluster = candidate
					}
				}
			}
		}
	}

	classifiedJobs, err := func() ([]projectedJob, error) {
		if !jobsClassified {
			jobsClassified = true
			jobsForProjection, jobsForProjectionErr = cv.classifyJobsForProjection(jc, path, config, jobVolumes)
		}
		return jobsForProjection, jobsForProjectionErr
	}()
	if err != nil {
		return "", err
	}
	previousCluster := previousClusterForProjectedJobs(classifiedJobs)
	if previousCluster != "" && previousCluster != cluster {
		previousInfo, previousExists := cv.clusterMap[previousCluster]
		previousProviderVolumes, previousProviderExists := cv.clusterVolumeMap[previousInfo.Provider]
		_, previousTracked := previousProviderVolumes[previousCluster]
		previousEligible := previousExists && previousProviderExists && previousTracked && previousInfo.Capacity > 0 && !cv.blocked.Has(previousCluster) &&
			(cloudProvider == "" || cloudProvider == previousInfo.Provider)
		if previousEligible {
			previousVolume, err := projectedVolume(previousCluster)
			if err != nil {
				return "", err
			}
			selectedInfo, selectedExists := cv.clusterMap[cluster]
			selectedProviderVolumes, selectedProviderExists := cv.clusterVolumeMap[selectedInfo.Provider]
			_, selectedTracked := selectedProviderVolumes[cluster]
			if selectedExists && selectedProviderExists && selectedTracked && selectedInfo.Capacity > 0 {
				selectedVolume, err := projectedVolume(cluster)
				if err != nil {
					return "", err
				}
				previousScore := (previousProviderVolumes[previousCluster] + previousVolume) / float64(previousInfo.Capacity)
				selectedScore := (selectedProviderVolumes[cluster] + selectedVolume) / float64(selectedInfo.Capacity)
				withinHysteresis := previousScore <= selectedScore*(1+cv.hysteresis)
				movedDemand := movedDemandForCandidate(cluster, string(config.Default), classifiedJobs)
				withinMovementBudget := cv.movementBudget < 0 || cv.movedDemand+movedDemand <= cv.movementBudget
				if withinHysteresis || !withinMovementBudget {
					cluster = previousCluster
				} else {
					cv.movedDemand += movedDemand
				}
			}
		}
	}

	var errs []error
	for _, k := range sortedStringKeys(jc.PresubmitsStatic) {
		for _, job := range jc.PresubmitsStatic[k] {
			if err := cv.addToVolume(cluster, job.JobBase, path, config, jobVolumes); err != nil {
				errs = append(errs, err)
			}
		}
	}

	for _, k := range sortedStringKeys(jc.PostsubmitsStatic) {
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

func (cv *clusterVolume) classifyJobsForProjection(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, jobVolumes map[string]float64) ([]projectedJob, error) {
	var jobsForProjection []projectedJob
	addProjectedJob := func(jobBase prowconfig.JobBase) error {
		determinedCluster, canBeRelocated, err := config.DetermineClusterForJob(jobBase, path, cv.clusterMap)
		if err != nil {
			return fmt.Errorf("failed to determine projected cluster for job %s in path %q: %w", jobBase.Name, path, err)
		}
		jobsForProjection = append(jobsForProjection, projectedJob{
			determinedCluster: string(determinedCluster),
			canBeRelocated:    canBeRelocated,
			blocked:           blockedClustersForJob(jobBase.Name, string(determinedCluster), cv.blocked),
			volume:            demandForJob(jobVolumes, jobBase.Name, cv.minimumJobDemand),
		})
		if previous, exists := cv.previousAssignments[jobBase.Name]; exists {
			jobsForProjection[len(jobsForProjection)-1].previousCluster = previous.Cluster
		}
		return nil
	}

	for _, k := range sortedStringKeys(jc.PresubmitsStatic) {
		for _, job := range jc.PresubmitsStatic[k] {
			if err := addProjectedJob(job.JobBase); err != nil {
				return nil, err
			}
		}
	}
	for _, k := range sortedStringKeys(jc.PostsubmitsStatic) {
		for _, job := range jc.PostsubmitsStatic[k] {
			if err := addProjectedJob(job.JobBase); err != nil {
				return nil, err
			}
		}
	}
	for _, job := range jc.Periodics {
		if err := addProjectedJob(job.JobBase); err != nil {
			return nil, err
		}
	}
	return jobsForProjection, nil
}

func projectedVolumeForCandidate(candidate, defaultCluster string, jobsForProjection []projectedJob) float64 {
	var volume float64
	for _, job := range jobsForProjection {
		target := dispatcher.DetermineTargetCluster(candidate, job.determinedCluster, defaultCluster, job.canBeRelocated, job.blocked)
		if target == candidate {
			volume += job.volume
		}
	}
	return volume
}

func movedDemandForCandidate(candidate, defaultCluster string, jobsForProjection []projectedJob) float64 {
	var volume float64
	for _, job := range jobsForProjection {
		if !job.canBeRelocated || job.previousCluster == "" {
			continue
		}
		target := dispatcher.DetermineTargetCluster(candidate, job.determinedCluster, defaultCluster, job.canBeRelocated, job.blocked)
		if target != job.previousCluster {
			volume += job.volume
		}
	}
	return volume
}

func stableClusterTieBreak(path, cluster string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cluster))
	return h.Sum64()
}

func repositoryRelativeTieBreakPath(prowJobConfigDir, path string) (string, error) {
	if prowJobConfigDir == "" {
		return filepath.ToSlash(path), nil
	}
	relativePath, err := filepath.Rel(prowJobConfigDir, path)
	if err != nil {
		return "", fmt.Errorf("failed to derive tie-break path for %q: %w", path, err)
	}
	return filepath.ToSlash(relativePath), nil
}

func extractCapabilities(labels map[string]string) []string {
	var capabilities []string
	prefix := "capability/"

	for key, value := range labels {
		if strings.HasPrefix(key, prefix) && strings.TrimPrefix(key, prefix) == value {
			capabilities = append(capabilities, value)
		}
	}
	sort.Strings(capabilities)

	return capabilities
}

func isBlockedClusterRelocationException(jobName string) bool {
	for _, re := range blockedClusterRelocationJobExceptions {
		if re.MatchString(jobName) {
			return true
		}
	}
	return false
}

func blockedClustersForJob(jobName string, determinedCluster string, blocked sets.Set[string]) sets.Set[string] {
	if !blocked.Has(determinedCluster) || !isBlockedClusterRelocationException(jobName) {
		return blocked
	}

	filteredBlocked := blocked.Clone()
	filteredBlocked.Delete(determinedCluster)
	return filteredBlocked
}

func findClusterAssigmentsForJobs(jc *prowconfig.JobConfig, prowJobConfigDir, path string, config *dispatcher.Config, pjs map[string]dispatcher.ProwJobData, blocked sets.Set[string], cm dispatcher.ClusterMap, minimumJobDemand float64) error {
	mostUsedCluster := dispatcher.FindMostUsedCluster(jc)
	group, err := repositoryRelativeTieBreakPath(prowJobConfigDir, path)
	if err != nil {
		return err
	}

	getClusterForMissingJob := func(cluster string, jobBase prowconfig.JobBase, pjs map[string]dispatcher.ProwJobData) error {
		determinedCluster, canBeRelocated, err := config.DetermineClusterForJob(jobBase, path, cm)
		if err != nil {
			return fmt.Errorf("failed to determine cluster for the job %s in path %q: %w", jobBase.Name, path, err)
		}

		blockedForJob := blockedClustersForJob(jobBase.Name, string(determinedCluster), blocked)
		c := dispatcher.DetermineTargetCluster(cluster, string(determinedCluster), string(config.Default), canBeRelocated, blockedForJob)
		demand := demandForJob(nil, jobBase.Name, minimumJobDemand)
		jobGroup := group
		if existing, exists := pjs[jobBase.Name]; exists {
			if existing.Demand > 0 && !math.IsNaN(existing.Demand) && !math.IsInf(existing.Demand, 0) {
				demand = existing.Demand
			}
			if existing.Group != "" {
				jobGroup = existing.Group
			}
		}
		pjs[jobBase.Name] = dispatcher.ProwJobData{Cluster: c, Capabilities: extractCapabilities(jobBase.Labels), Demand: demand, Group: jobGroup}
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

	blockedForJob := blockedClustersForJob(jobBase.Name, string(determinedCluster), cv.blocked)
	c := dispatcher.DetermineTargetCluster(cluster, string(determinedCluster), string(config.Default), canBeRelocated, blockedForJob)
	group, err := repositoryRelativeTieBreakPath(cv.prowJobConfigDir, path)
	if err != nil {
		return err
	}
	demand := demandForJob(jobVolumes, jobBase.Name, cv.minimumJobDemand)
	cv.pjs[jobBase.Name] = dispatcher.ProwJobData{Cluster: c, Capabilities: extractCapabilities(jobBase.Labels), Demand: demand, Group: group}
	if determinedCloudProvider := config.IsInBuildFarm(api.Cluster(c)); determinedCloudProvider != "" {
		cv.clusterVolumeMap[string(determinedCloudProvider)][c] = cv.clusterVolumeMap[string(determinedCloudProvider)][c] + demand
		return nil
	}
	cv.specialClusters[c] = cv.specialClusters[c] + demand
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

type clusterConfigReconciler struct {
	observedClusterMap       dispatcher.ClusterMap
	observedBlocked          sets.Set[string]
	hasObserved              bool
	publishedInventoryDigest string
}

// reconcile publishes a changed cluster configuration and advances observed
// state only after publication succeeds.
func (r *clusterConfigReconciler) reconcile(clusterMap dispatcher.ClusterMap, blocked sets.Set[string], dispatch func(bool) error) (bool, error) {
	currentInventoryDigest, err := clusterInventoryDigest(clusterMap, blocked)
	if err != nil {
		return false, fmt.Errorf("failed to calculate cluster inventory digest: %w", err)
	}
	if r.hasObserved && reflect.DeepEqual(clusterMap, r.observedClusterMap) && reflect.DeepEqual(blocked, r.observedBlocked) &&
		currentInventoryDigest == r.publishedInventoryDigest {
		return false, nil
	}

	forceDispatch := currentInventoryDigest != r.publishedInventoryDigest ||
		r.hasObserved && dispatcher.HasCapacityOrCapabilitiesChanged(r.observedClusterMap, clusterMap)
	if err := dispatch(forceDispatch); err != nil {
		return true, err
	}

	r.observedClusterMap = clusterMap
	r.observedBlocked = blocked
	r.hasObserved = true
	r.publishedInventoryDigest = currentInventoryDigest
	return true, nil
}

type inventoryDigestCluster struct {
	Name         string   `json:"name"`
	Provider     string   `json:"provider"`
	Capacity     int      `json:"capacity"`
	Capabilities []string `json:"capabilities"`
}

type inventoryDigestInput struct {
	Version  int                      `json:"version"`
	Clusters []inventoryDigestCluster `json:"clusters"`
	Blocked  []string                 `json:"blocked"`
}

func clusterInventoryDigest(clusterMap dispatcher.ClusterMap, blocked sets.Set[string]) (string, error) {
	return clusterInventoryDigestForVersion(clusterMap, blocked, inventoryDigestVersion)
}

func clusterInventoryDigestForVersion(clusterMap dispatcher.ClusterMap, blocked sets.Set[string], version int) (string, error) {
	clusterNames := make([]string, 0, len(clusterMap))
	for clusterName := range clusterMap {
		clusterNames = append(clusterNames, clusterName)
	}
	sort.Strings(clusterNames)

	clusters := make([]inventoryDigestCluster, 0, len(clusterNames))
	for _, clusterName := range clusterNames {
		clusterInfo := clusterMap[clusterName]
		capabilities := append([]string(nil), clusterInfo.Capabilities...)
		sort.Strings(capabilities)
		clusters = append(clusters, inventoryDigestCluster{
			Name:         clusterName,
			Provider:     clusterInfo.Provider,
			Capacity:     clusterInfo.Capacity,
			Capabilities: capabilities,
		})
	}

	blockedClusters := sets.List(blocked)
	if blockedClusters == nil {
		blockedClusters = []string{}
	}
	input, err := json.Marshal(inventoryDigestInput{Version: version, Clusters: clusters, Blocked: blockedClusters})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:]), nil
}

func publishedInventoryDigestPath(jobsStoragePath string) string {
	return jobsStoragePath + ".inventory-digest"
}

func readPublishedInventoryDigest(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read published inventory digest: %w", err)
	}
	digest := strings.TrimSpace(string(data))
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", false, nil
	}
	return digest, true, nil
}

func writePublishedInventoryDigest(path, digest string) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("failed to create temporary inventory digest: %w", err)
	}
	temporaryName := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryName)
	}()

	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("failed to set permissions on temporary inventory digest: %w", err)
	}
	if _, err := temporary.WriteString(digest + "\n"); err != nil {
		return fmt.Errorf("failed to write temporary inventory digest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary inventory digest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary inventory digest: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("failed to atomically replace inventory digest: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("failed to sync inventory digest directory: %w", err)
	}
	return nil
}

func invalidatePublishedInventoryDigest(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to remove published inventory digest: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to sync published inventory digest directory: %w", err)
	}
	return nil
}

func writeFullDispatchAssignments(jobsStoragePath string, pjs map[string]dispatcher.ProwJobData, writeGob func(string, interface{}) error) error {
	if err := invalidatePublishedInventoryDigest(publishedInventoryDigestPath(jobsStoragePath)); err != nil {
		return fmt.Errorf("failed to invalidate cluster inventory digest: %w", err)
	}
	return writeGob(jobsStoragePath, pjs)
}

// fullDispatchController serializes complete generations and remembers a failed
// generation regardless of whether it was triggered by config reconciliation,
// the weekly cron, or the manual event endpoint.
type fullDispatchController struct {
	mu           sync.Mutex
	dispatch     func(bool) error
	retryPending bool
}

func (c *fullDispatchController) reconcile(force bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.dispatch(force || c.retryPending)
	c.retryPending = err != nil
	return err
}

func (c *fullDispatchController) hasPendingRetry() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retryPending
}

func fallbackPublicationMarkerPath(jobsStoragePath string) string {
	return jobsStoragePath + ".fallback-pending"
}

func fallbackPublicationPending(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect fallback publication marker: %w", err)
	}
	return true, nil
}

func markFallbackPublicationPending(path string) error {
	marker, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create fallback publication marker: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = marker.Close()
		}
	}()

	if _, err := marker.WriteString(time.Now().UTC().Format(time.RFC3339Nano) + "\n"); err != nil {
		return fmt.Errorf("failed to write fallback publication marker: %w", err)
	}
	if err := marker.Sync(); err != nil {
		return fmt.Errorf("failed to sync fallback publication marker: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("failed to close fallback publication marker: %w", err)
	}
	closed = true
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to sync fallback publication marker directory: %w", err)
	}
	return nil
}

func clearFallbackPublicationPending(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to remove fallback publication marker: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to sync fallback publication marker directory: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// dispatchJobs loads the Prow jobs and chooses a cluster in the build farm if possible.
// The current implementation walks through the Prow Job config files.
// For each file, it tries to assign all jobs in it to a cluster in the build farm.
//   - When all the e2e tests are targeting the same cloud provider, we run the test pod on the that cloud provider too.
//   - When the e2e tests are targeting different cloud providers, or there is no e2e tests at all, we can run the tests
//     on any cluster in the build farm. Those jobs are used to load balance the workload of clusters in the build farm.
func dispatchJobs(prowJobConfigDir string, config *dispatcher.Config, jobVolumes map[string]float64, blocked sets.Set[string], volumeDistribution map[string]float64, cm dispatcher.ClusterMap) (map[string]dispatcher.ProwJobData, error) {
	return dispatchJobsWithPolicy(prowJobConfigDir, config, jobVolumes, blocked, volumeDistribution, cm, nil, 0, -1, 1)
}

func dispatchJobsWithPolicy(prowJobConfigDir string, config *dispatcher.Config, jobVolumes map[string]float64, blocked sets.Set[string], volumeDistribution map[string]float64, cm dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData, hysteresisPercent, movementBudgetPercent, minimumJobDemand float64) (map[string]dispatcher.ProwJobData, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// cv stores the volume for each cluster in the build farm
	cv := &clusterVolume{
		clusterVolumeMap:    map[string]map[string]float64{},
		cloudProviders:      sets.New[string](),
		pjs:                 map[string]dispatcher.ProwJobData{},
		blocked:             blocked,
		specialClusters:     map[string]float64{},
		volumeDistribution:  volumeDistribution,
		clusterMap:          cm,
		prowJobConfigDir:    prowJobConfigDir,
		previousAssignments: previous,
		hysteresis:          hysteresisPercent / 100,
		movementBudget:      -1,
		minimumJobDemand:    minimumJobDemand,
	}
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

	sort.Slice(fileList, func(i, j int) bool {
		if fileList[i].size != fileList[j].size {
			return fileList[i].size > fileList[j].size
		}
		return fileList[i].path < fileList[j].path
	})
	if movementBudgetPercent >= 0 {
		movementBudget, err := movementBudgetForConfiguredJobFiles(fileList, jobVolumes, movementBudgetPercent, minimumJobDemand)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate movement budget from configured jobs: %w", err)
		}
		cv.movementBudget = movementBudget
	}
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

func dispatchDeltaJobs(prowJobConfigDir string, config *dispatcher.Config, blocked sets.Set[string], pjs map[string]dispatcher.ProwJobData, cm dispatcher.ClusterMap, minimumJobDemand float64) error {
	var errs []error
	dispatch := func(jobConfig *prowconfig.JobConfig, path string, info fs.DirEntry) {
		if err := findClusterAssigmentsForJobs(jobConfig, prowJobConfigDir, path, config, pjs, blocked, cm, minimumJobDemand); err != nil {
			errs = append(errs, err)
		}
	}
	fileList, err := composeFileInfoList(prowJobConfigDir)
	if err != nil {
		return fmt.Errorf("failed to dispatch all Prow jobs: %w", err)
	}

	sort.Slice(fileList, func(i, j int) bool {
		if fileList[i].size != fileList[j].size {
			return fileList[i].size > fileList[j].size
		}
		return fileList[i].path < fileList[j].path
	})
	if err := dispatchEveryFile(fileList, dispatch); err != nil {
		errs = append(errs, err)
	}
	return utilerrors.NewAggregate(errs)
}

func assignmentsRequireMetadataMigration(assignments map[string]dispatcher.ProwJobData) bool {
	for _, assignment := range assignments {
		if assignment.Demand <= 0 || math.IsNaN(assignment.Demand) || math.IsInf(assignment.Demand, 0) || assignment.Group == "" {
			return true
		}
	}
	return false
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

func addConfiguredJobNames(jobConfig *prowconfig.JobConfig, jobNames sets.Set[string]) {
	for _, jobs := range jobConfig.PresubmitsStatic {
		for _, job := range jobs {
			jobNames.Insert(job.Name)
		}
	}
	for _, jobs := range jobConfig.PostsubmitsStatic {
		for _, job := range jobs {
			jobNames.Insert(job.Name)
		}
	}
	for _, job := range jobConfig.Periodics {
		jobNames.Insert(job.Name)
	}
}

func movementBudgetForConfiguredJobFiles(fileList []fileSizeInfo, jobVolumes map[string]float64, movementBudgetPercent, minimumJobDemand float64) (float64, error) {
	jobNames := sets.New[string]()
	if err := dispatchEveryFile(fileList, func(jobConfig *prowconfig.JobConfig, _ string, _ fs.DirEntry) {
		addConfiguredJobNames(jobConfig, jobNames)
	}); err != nil {
		return 0, err
	}
	return movementBudgetForConfiguredJobs(jobNames, jobVolumes, movementBudgetPercent, minimumJobDemand), nil
}

func movementBudgetForConfiguredJobs(jobNames sets.Set[string], jobVolumes map[string]float64, movementBudgetPercent, minimumJobDemand float64) float64 {
	var totalDemand float64
	for _, jobName := range sets.List(jobNames) {
		totalDemand += demandForJob(jobVolumes, jobName, minimumJobDemand)
	}
	return totalDemand * movementBudgetPercent / 100
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

func clustersMapToSet(clusterMap dispatcher.ClusterMap) sets.Set[string] {
	clusterSet := sets.Set[string]{}
	for cluster := range clusterMap {
		clusterSet.Insert(cluster)
	}
	return clusterSet
}

func gitCloneRelease(targetDir string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--single-branch", "https://github.com/openshift/release.git")
	cmd.Dir = targetDir

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

func withRestoredWorkingDirectory(operation func() error) (retErr error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to determine current working directory: %w", err)
	}
	defer func() {
		if err := os.Chdir(workingDirectory); err != nil {
			retErr = utilerrors.NewAggregate([]error{retErr, fmt.Errorf("failed to restore working directory %q: %w", workingDirectory, err)})
		}
	}()

	return operation()
}

// createPR creates or updates the fallback assignment PR. Errors are returned to
// reconciliation so the same generation can be retried without terminating the service.
func createPR(o options, config *dispatcher.Config, pjs map[string]dispatcher.ProwJobData, cm dispatcher.ClusterMap) error {
	targetDirWithRelease := filepath.Join(o.targetDir, "/release")
	cleanup(targetDirWithRelease)
	defer cleanup(targetDirWithRelease)

	if err := gitCloneRelease(o.targetDir); err != nil {
		return fmt.Errorf("failed to clone release repository: %w", err)
	}

	if err := dispatcher.SaveConfig(config, filepath.Join(targetDirWithRelease, "/core-services/sanitize-prow-jobs/_config.yaml")); err != nil {
		return fmt.Errorf("failed to save config file %q: %w", o.configPath, err)
	}

	if err := sanitizer.DeterminizeJobs(filepath.Join(targetDirWithRelease, "/ci-operator/jobs"), config, pjs, make(sets.Set[string]), cm); err != nil {
		return fmt.Errorf("failed to determinize jobs: %w", err)
	}

	title := fmt.Sprintf("%s at %s", matchTitle, time.Now().Format(time.RFC1123))
	if err := withRestoredWorkingDirectory(func() error {
		return o.PRCreationOptions.UpsertPR(targetDirWithRelease, githubOrg, githubRepo, o.upstreamBranch, title, prcreation.PrAssignee(o.assign), prcreation.MatchTitle(matchTitle), prcreation.AdditionalLabels([]string{rehearse.RehearsalsAckLabel, "priority/ci-critical"}), prcreation.PrBody(o.prBody))
	}); err != nil {
		return fmt.Errorf("failed to upsert PR: %w", err)
	}
	return nil
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

	if o.PrometheusOptions.PrometheusPasswordPath != "" {
		if err := secret.Add(o.PrometheusOptions.PrometheusPasswordPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	if o.PrometheusOptions.PrometheusBearerTokenPath != "" {
		if err := secret.Add(o.PrometheusOptions.PrometheusBearerTokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to start secrets agent")
		}
	}

	promVolumes, err := dispatcher.NewPrometheusVolumes(o.PrometheusOptions, o.prometheusDaysBefore)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prometheus volumes")
	}

	secretPaths := []string{o.slackTokenPath}
	if o.controlAPITokenPath != "" {
		secretPaths = append(secretPaths, o.controlAPITokenPath)
	}
	if o.fallbackObserverTokenPath != "" {
		secretPaths = append(secretPaths, o.fallbackObserverTokenPath)
	}
	if err := secret.Add(secretPaths...); err != nil {
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
	var fullDispatch *fullDispatchController
	prowjobs := dispatcher.NewProwjobs(o.jobsStoragePath)
	snapshots := dispatcher.NewSnapshotManager(o.snapshotStoragePath)
	if err := snapshots.Load(); err != nil {
		if !os.IsNotExist(err) {
			logrus.WithError(err).Warn("ignoring invalid local policy snapshot; it will be rebuilt from durable inputs")
		}
	}

	var controlPlane *dispatcher.ControlPlane
	if o.overrideNamespace != "" {
		restConfig, err := util.LoadClusterConfig()
		if err != nil {
			logrus.WithError(err).Fatal("failed to load Kubernetes configuration for DispatchOverride storage")
		}
		scheme := runtime.NewScheme()
		if err := dispatcherv1.AddToScheme(scheme); err != nil {
			logrus.WithError(err).Fatal("failed to register DispatchOverride API")
		}
		kubeClient, err := ctrlruntimeclient.New(restConfig, ctrlruntimeclient.Options{Scheme: scheme})
		if err != nil {
			logrus.WithError(err).Fatal("failed to create DispatchOverride client")
		}
		controlPlane, err = dispatcher.NewControlPlane(snapshots, dispatcher.NewKubernetesOverrideStore(kubeClient, o.overrideNamespace), dispatcher.ControlOptions{
			AllowedChannelID: o.dispatchCommandChannelID, MaxTTL: o.maxOverrideTTL, MaxDrainTTL: o.maxDrainTTL,
			PlanTTL: o.planTTL, AffectedDemandApproval: o.affectedDemandApprovalThreshold,
			EnableCapacity: o.enableRuntimeCapacity, EnableDrain: o.enableRuntimeDrain, EnableCapabilityScope: o.enableRuntimeCapabilityScope,
			SchedulerPropagationBound: o.schedulerPropagationBound,
			WriteSafetyCheck:          o.schedulerWriteSafetyCheck(),
		})
		if err != nil {
			logrus.WithError(err).Fatal("failed to initialize dispatcher control plane")
		}
		go controlPlane.Run(context.Background())
	}

	publishSnapshot := func(baseline map[string]dispatcher.ProwJobData, clusterMap dispatcher.ClusterMap, blocked sets.Set[string]) error {
		if controlPlane != nil {
			controlPlane.UpdateBaseline(baseline, clusterMap, blocked)
			return controlPlane.Reconcile(context.Background())
		}
		generation := uint64(1)
		current := snapshots.Current()
		if current != nil {
			generation = current.Generation + 1
		}
		snapshot, _, err := dispatcher.CompileSnapshot(dispatcher.CompileInput{Baseline: baseline, Inventory: clusterMap, Blocked: blocked, Generation: generation, Now: time.Now().UTC()})
		if err != nil {
			return err
		}
		if current != nil && current.InputDigest == snapshot.InputDigest && snapshots.Ready() {
			return nil
		}
		return snapshots.Publish(snapshot)
	}
	c := cron.New()

	// Pass an empty cluster list to it. This works as long as it's guaranteed that the
	// dispatchWrapper func below is called one when this program starts.
	ecd := dispatcher.NewEphemeralClusterDispatcher([]string{})

	{
		var mu sync.Mutex
		slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))

		dispatchDelta := func() error {
			mu.Lock()
			defer mu.Unlock()
			config, err := dispatcher.LoadConfig(o.configPath)
			if err != nil {
				return fmt.Errorf("failed to load config from %q: %w", o.configPath, err)
			}
			cm, blocked, err := dispatcher.LoadClusterConfig(o.clusterConfigPath)
			if err != nil {
				return fmt.Errorf("failed to load cluster config: %w", err)
			}
			if _, err := config.SynchronizeBuildFarm(cm); err != nil {
				return fmt.Errorf("failed to synchronize build farm: %w", err)
			}

			pjs := prowjobs.GetDataCopy()

			if err := dispatchDeltaJobs(o.prowJobConfigDir, config, blocked, pjs, cm, o.MinimumJobDemand); err != nil {
				return fmt.Errorf("failed to dispatch delta jobs: %w", err)
			}
			if err := dispatcher.WriteGob(o.jobsStoragePath, pjs); err != nil {
				if !dispatcher.IsGobWriteCommitted(err) {
					return fmt.Errorf("failed to persist delta job assignments: %w", err)
				}
				prowjobs.Regenerate(pjs)
				return utilerrors.NewAggregate([]error{
					fmt.Errorf("delta job assignments were replaced but their directory sync failed: %w", err),
					publishSnapshot(pjs, cm, blocked),
				})
			}
			prowjobs.Regenerate(pjs)
			return publishSnapshot(pjs, cm, blocked)
		}

		dispatch := func(forceDispatch bool) error {
			mu.Lock()
			defer mu.Unlock()

			config, err := dispatcher.LoadConfig(o.configPath)
			if err != nil {
				return fmt.Errorf("failed to load config from %q: %w", o.configPath, err)
			}

			configClusterMap, blocked, err := dispatcher.LoadClusterConfig(o.clusterConfigPath)
			if err != nil {
				return fmt.Errorf("failed to load cluster config: %w", err)
			}
			inventoryDigest, err := clusterInventoryDigest(configClusterMap, blocked)
			if err != nil {
				return fmt.Errorf("failed to calculate cluster inventory digest: %w", err)
			}
			clustersFromConfig := clustersMapToSet(configClusterMap)
			inventoryChanged, err := config.SynchronizeBuildFarm(configClusterMap)
			if err != nil {
				return fmt.Errorf("failed to synchronize build farm: %w", err)
			}

			newBlockedClusters := prowjobs.HasAnyOfClusters(blocked)
			currentAssignments := prowjobs.GetDataCopy()
			missingAssignments := len(currentAssignments) == 0
			metadataMigrationRequired := assignmentsRequireMetadataMigration(currentAssignments)

			if !forceDispatch && !inventoryChanged && !newBlockedClusters && !missingAssignments && !metadataMigrationRequired {
				// The ephemeral scheduler is not persisted in the Gob cache, so it still
				// needs initialization after a restart when no generation is necessary.
				ecd.Reset(sets.List(clustersFromConfig))
				return publishSnapshot(currentAssignments, configClusterMap, blocked)
			}

			jobVolumes, err := promVolumes.GetJobVolumes()
			if err != nil {
				return fmt.Errorf("failed to get job volumes: %w", err)
			}

			pjs, err := dispatchJobsWithPolicy(
				o.prowJobConfigDir, config, jobVolumes, blocked, promVolumes.CalculateVolumeDistribution(configClusterMap), configClusterMap,
				currentAssignments, o.rebalanceHysteresisPercent, o.movementBudgetPercent, o.MinimumJobDemand,
			)
			if err != nil {
				return fmt.Errorf("failed to dispatch jobs: %w", err)
			}

			markerPath := fallbackPublicationMarkerPath(o.jobsStoragePath)
			if o.createPR {
				if err := markFallbackPublicationPending(markerPath); err != nil {
					return err
				}
			}

			var committedWriteErr error
			if err := writeFullDispatchAssignments(o.jobsStoragePath, pjs, dispatcher.WriteGob); err != nil {
				if !dispatcher.IsGobWriteCommitted(err) {
					return fmt.Errorf("failed to persist job assignments: %w", err)
				}
				committedWriteErr = fmt.Errorf("job assignments were replaced but their directory sync failed: %w", err)
			}
			prowjobs.Regenerate(pjs)
			ecd.Reset(sets.List(clustersFromConfig))
			if err := publishSnapshot(pjs, configClusterMap, blocked); err != nil {
				return utilerrors.NewAggregate([]error{committedWriteErr, fmt.Errorf("failed to publish policy snapshot: %w", err)})
			}
			if committedWriteErr == nil {
				if err := writePublishedInventoryDigest(publishedInventoryDigestPath(o.jobsStoragePath), inventoryDigest); err != nil {
					return fmt.Errorf("failed to publish cluster inventory digest: %w", err)
				}
			}

			if o.createPR {
				if err := createPR(o, config, pjs, configClusterMap); err != nil {
					return utilerrors.NewAggregate([]error{committedWriteErr, err})
				}
				if err := clearFallbackPublicationPending(markerPath); err != nil {
					return utilerrors.NewAggregate([]error{committedWriteErr, err})
				}
				if err := sendSlackMessage(slackClient, o.opsChannelId); err != nil {
					logrus.WithError(err).Error("Failed to post message in ops channel")
				}
			}
			return committedWriteErr
		}

		fallbackPending := false
		if o.createPR {
			var err error
			fallbackPending, err = fallbackPublicationPending(fallbackPublicationMarkerPath(o.jobsStoragePath))
			if err != nil {
				logrus.WithError(err).Fatal("failed to determine fallback publication state")
			}
			if fallbackPending {
				logrus.Warn("found an incomplete fallback publication; forcing a full dispatch")
			}
		}
		fullDispatch = &fullDispatchController{dispatch: dispatch, retryPending: fallbackPending}

		dispatchWrapper = func(forceDispatch bool) {
			if err := fullDispatch.reconcile(forceDispatch); err != nil {
				logrus.WithError(err).Error("failed to reconcile job assignments")
			}
		}
		dispatchDeltaWrapper = func() {
			if err := dispatchDelta(); err != nil {
				logrus.WithError(err).Error("failed to reconcile delta job assignments")
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

		publishedInventoryDigest, validPublishedInventoryDigest, err := readPublishedInventoryDigest(publishedInventoryDigestPath(o.jobsStoragePath))
		if err != nil {
			logrus.WithError(err).Error("failed to load published cluster inventory digest; forcing a full dispatch")
		} else if !validPublishedInventoryDigest {
			logrus.Warn("published cluster inventory digest is missing or invalid; forcing a full dispatch")
		}
		clusterConfigState := &clusterConfigReconciler{publishedInventoryDigest: publishedInventoryDigest}

		reconcileClusterConfig := func() {
			currentConfigClusterMap, currentBlocked, err := dispatcher.LoadClusterConfig(config)
			if err != nil {
				logrus.WithError(err).Error("failed to load cluster config")
				return
			}
			attempted, err := clusterConfigState.reconcile(currentConfigClusterMap, currentBlocked, func(forceDispatch bool) error {
				logrus.WithField("prevConfigClusterMap", clusterConfigState.observedClusterMap).WithField("prevBlocked", clusterConfigState.observedBlocked).
					WithField("currentConfigClusterMap", currentConfigClusterMap).WithField("currentBlocked", currentBlocked).Info("reconciling cluster config")
				return fullDispatch.reconcile(forceDispatch)
			})
			if err != nil {
				logrus.WithError(err).Error("failed to reconcile cluster config; the generation will be retried")
				return
			}
			if !attempted && fullDispatch.hasPendingRetry() {
				if err := fullDispatch.reconcile(true); err != nil {
					logrus.WithError(err).Error("failed to retry full job assignment generation")
				}
			}
		}

		// Run dispatch for the first time. A failure is retried by the same loop.
		reconcileClusterConfig()

		for {
			select {
			case <-configTicker.C:
				reconcileClusterConfig()

			case <-deltaTicker.C:
				dispatchDeltaWrapper()
			}
		}
	}(o.clusterConfigPath)

	dispatchServer := dispatcher.NewSnapshotServer(prowjobs, ecd, dispatchWrapper, snapshots)
	mux := http.NewServeMux()
	mux.HandleFunc("/", dispatchServer.RequestHandler)
	mux.HandleFunc("/healthz", dispatchServer.HealthHandler)
	mux.HandleFunc("/readyz", dispatchServer.ReadyHandler)
	mux.Handle("/metrics", promhttp.Handler())
	if o.legacyEventEndpoint {
		mux.HandleFunc("/event", dispatchServer.EventHandler)
	}
	if controlPlane != nil {
		mux.Handle("/control/v1/", dispatcher.NewControlServer(controlPlane, secret.GetTokenGenerator(o.controlAPITokenPath)))
		if o.fallbackObserverTokenPath != "" {
			mux.Handle("/fallback-observer/v1/protection", dispatcher.NewFallbackObservationServer(controlPlane, secret.GetTokenGenerator(o.fallbackObserverTokenPath)))
		}
	}
	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	logrus.Fatal(httpServer.ListenAndServe())
}
