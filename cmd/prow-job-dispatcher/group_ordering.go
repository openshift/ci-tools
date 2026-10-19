package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// validateGroupOrders rejects misspelled policies before publishing assignments.
func validateGroupOrders(selected, shadow string) error {
	if selected != "" && selected != "file-size" && selected != "demand" {
		return fmt.Errorf("--group-order must be file-size or demand")
	}
	if shadow != "" && shadow != "disabled" && shadow != "file-size" && shadow != "demand" {
		return fmt.Errorf("--shadow-group-order must be file-size, demand, or disabled")
	}
	return nil
}

// jobBases visits all configured jobs in stable order for reproducible demand sums.
func jobBases(config *prowconfig.JobConfig) []prowconfig.JobBase {
	var jobs []prowconfig.JobBase
	for _, key := range sortedStringKeys(config.PresubmitsStatic) {
		for _, job := range config.PresubmitsStatic[key] {
			jobs = append(jobs, job.JobBase)
		}
	}
	for _, key := range sortedStringKeys(config.PostsubmitsStatic) {
		for _, job := range config.PostsubmitsStatic[key] {
			jobs = append(jobs, job.JobBase)
		}
	}
	for _, job := range config.Periodics {
		jobs = append(jobs, job.JobBase)
	}
	return jobs
}

// loadDispatchFiles freezes file contents for both policies in a comparison.
func loadDispatchFiles(root string) ([]fileSizeInfo, error) {
	files, err := composeFileInfoList(root)
	if err != nil {
		return nil, err
	}
	for i := range files {
		data, err := gzip.ReadFileMaybeGZIP(files[i].path)
		if err != nil {
			return nil, fmt.Errorf("reading job config %q: %w", files[i].path, err)
		}
		config := &prowconfig.JobConfig{}
		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("decoding job config %q: %w", files[i].path, err)
		}
		files[i].jobConfig = config
	}
	return files, nil
}

// orderDispatchFiles counts only movable demand, since pinned jobs cannot benefit from ordering.
func orderDispatchFiles(input []fileSizeInfo, order string, config *dispatcher.Config, volumes map[string]float64, inventory dispatcher.ClusterMap, minimum float64, previous map[string]dispatcher.ProwJobData) ([]fileSizeInfo, error) {
	files := append([]fileSizeInfo(nil), input...)
	for i := range files {
		files[i].demand = 0
		if order != "demand" {
			continue
		}
		for _, job := range jobBases(files[i].jobConfig) {
			if previous[job.Name].Affinity.Cluster != "" {
				continue
			}
			_, movable, err := config.DetermineClusterForJob(job, files[i].path, inventory)
			if err != nil {
				return nil, fmt.Errorf("classifying group %q for ordering: %w", files[i].path, err)
			}
			if movable {
				files[i].demand += demandForJob(volumes, job.Name, minimum)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if order == "demand" && files[i].demand != files[j].demand {
			return files[i].demand > files[j].demand
		}
		if order != "demand" && files[i].size != files[j].size {
			return files[i].size > files[j].size
		}
		return files[i].path < files[j].path
	})
	return files, nil
}

// placementDigest tracks restrictions independently of the selected baseline assignment.
func placementDigest(job prowconfig.JobBase, determined string, movable bool, group string, config *dispatcher.Config) string {
	if movable {
		determined = ""
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%q %t %q %q %q", determined, movable, group, config.DetermineCloudMapping(job), extractCapabilities(job.Labels))
	return hex.EncodeToString(hash.Sum(nil))
}

// refreshJobAssignment preserves demand and placement unless current restrictions changed.
func refreshJobAssignment(job prowconfig.JobBase, candidate, group, path string, config *dispatcher.Config, assignments map[string]dispatcher.ProwJobData, blocked sets.Set[string], inventory dispatcher.ClusterMap, minimum float64) error {
	determined, movable, err := config.DetermineClusterForJob(job, path, inventory)
	if err != nil {
		return fmt.Errorf("determining cluster for job %q: %w", job.Name, err)
	}
	digest := placementDigest(job, string(determined), movable, group, config)
	existing, exists := assignments[job.Name]
	if exists && existing.PlacementKnown && existing.PlacementDigest == digest && existing.Group == group {
		return nil
	}
	demand := demandForJob(nil, job.Name, minimum)
	if existing.Demand > 0 && !math.IsNaN(existing.Demand) && !math.IsInf(existing.Demand, 0) {
		demand = existing.Demand
	}
	target := dispatcher.DetermineTargetCluster(candidate, string(determined), string(config.Default), movable, blockedClustersForJob(job.Name, string(determined), blocked))
	assignments[job.Name] = dispatcher.ProwJobData{Cluster: target, Demand: demand, Group: group, Capabilities: extractCapabilities(job.Labels), PlacementKnown: true, Relocatable: movable, PlacementDigest: digest}
	return nil
}

// boundClusterForConfig prevents one reuse unit from being split across namespace hosts.
func boundClusterForConfig(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, previous map[string]dispatcher.ProwJobData, inventory dispatcher.ClusterMap, blocked sets.Set[string]) (string, error) {
	preferredProvider := ""
	clouds := getCloudProvidersForE2ETests(jc)
	if clouds.Len() == 1 {
		provider := sets.List(clouds)[0]
		// Match the planner's fallback when the requested cloud has no farm pool.
		if len(config.BuildFarm[api.Cloud(provider)]) > 0 {
			preferredProvider = provider
		}
	}
	bound := ""
	for _, job := range jobBases(jc) {
		_, movable, err := config.DetermineClusterForJob(job, path, inventory)
		if err != nil {
			return "", err
		}
		affinity := previous[job.Name].Affinity.Cluster
		if !movable || affinity == "" {
			continue
		}
		info, exists := inventory[affinity]
		if !exists || info.Capacity <= 0 || blocked.Has(affinity) || config.IsInBuildFarm(api.Cluster(affinity)) == "" {
			return "", fmt.Errorf("affinity host %q for job %q is unavailable", affinity, job.Name)
		}
		if preferredProvider != "" && preferredProvider != info.Provider {
			return "", fmt.Errorf("affinity host %q is incompatible with group cloud in %q", affinity, path)
		}
		if bound != "" && bound != affinity {
			return "", fmt.Errorf("group %q has incompatible affinity hosts %q and %q", path, bound, affinity)
		}
		bound = affinity
	}
	return bound, nil
}

// prechargeFixedDemand exposes immovable load before the first greedy placement.
func (cv *clusterVolume) prechargeFixedDemand(files []fileSizeInfo, config *dispatcher.Config, volumes map[string]float64) error {
	cv.prechargedJobs = sets.New[string]()
	for _, file := range files {
		if _, err := boundClusterForConfig(file.jobConfig, file.path, config, cv.previousAssignments, cv.clusterMap, cv.blocked); err != nil {
			return err
		}
		for _, job := range jobBases(file.jobConfig) {
			cluster, movable, err := config.DetermineClusterForJob(job, file.path, cv.clusterMap)
			if err != nil {
				return err
			}
			affinity := cv.previousAssignments[job.Name].Affinity.Cluster
			if movable && affinity == "" {
				continue
			}
			if !movable && blockedClustersForJob(job.Name, string(cluster), cv.blocked).Has(string(cluster)) {
				continue
			}
			target := dispatcher.DetermineTargetCluster("", string(cluster), string(config.Default), movable, blockedClustersForJob(job.Name, string(cluster), cv.blocked))
			if movable && affinity != "" {
				target = affinity
			}
			if cv.prechargedJobs.Has(job.Name) {
				continue
			}
			cv.prechargedJobs.Insert(job.Name)
			demand := demandForJob(volumes, job.Name, cv.minimumJobDemand)
			provider := config.IsInBuildFarm(api.Cluster(target))
			if provider != "" {
				cv.clusterVolumeMap[string(provider)][target] += demand
				continue
			}
			cv.specialClusters[target] += demand
		}
	}
	return nil
}
