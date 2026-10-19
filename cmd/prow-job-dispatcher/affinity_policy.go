package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

// validateAffinityOptions prevents accidental state-file replacement and unsafe refresh cadence.
func validateAffinityOptions(o options) error {
	if o.rebalanceInterval < 0 || o.rebalanceInterval > 0 && o.rebalanceInterval < 5*time.Minute {
		return errors.New("--rebalance-interval must be zero or at least 5m")
	}
	if o.rebalanceInterval > 0 && o.affinityStoragePath == "" {
		return errors.New("--rebalance-interval requires --group-affinity-storage-path")
	}
	if o.affinityStoragePath == "" {
		return nil
	}
	affinityPath, err := filepath.Abs(o.affinityStoragePath)
	if err != nil {
		return fmt.Errorf("resolve affinity path: %w", err)
	}
	snapshotPath := o.snapshotStoragePath
	if snapshotPath == "" && o.jobsStoragePath != "" {
		snapshotPath = o.jobsStoragePath + ".snapshot.json"
	}
	for _, path := range []string{o.jobsStoragePath, snapshotPath, o.configPath, o.clusterConfigPath} {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve dispatcher state path: %w", err)
		}
		if affinityPath == absolute {
			return fmt.Errorf("affinity storage must use a separate path from %q", path)
		}
	}
	return nil
}

// affinityPrevious freezes configured fallback hosts before any plan can replace them.
// New legacy jobs are seeded from their configured host too, since Prow can use
// Spec.Cluster without contacting this process when the dispatcher is unavailable.
func affinityPrevious(store *dispatcher.AffinityStore, root string, config *dispatcher.Config, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData, minimum float64, snapshotPath string) (map[string]dispatcher.ProwJobData, error) {
	if store == nil {
		return previous, nil
	}
	files, err := loadDispatchFiles(root)
	if err != nil {
		return nil, err
	}
	return affinityPreviousFromFiles(store, root, files, config, inventory, previous, minimum, snapshotPath)
}

// affinityPreviousFromFiles shares the exact configuration snapshot with planning.
func affinityPreviousFromFiles(store *dispatcher.AffinityStore, root string, files []fileSizeInfo, config *dispatcher.Config, inventory dispatcher.ClusterMap, previous map[string]dispatcher.ProwJobData, minimum float64, snapshotPath string) (map[string]dispatcher.ProwJobData, error) {
	if store == nil {
		return previous, nil
	}
	current := make(map[string]dispatcher.ProwJobData)
	for _, file := range files {
		group, err := repositoryRelativeTieBreakPath(root, file.path)
		if err != nil {
			return nil, err
		}
		for _, job := range jobBases(file.jobConfig) {
			if job.Agent != "" && job.Agent != "kubernetes" {
				continue
			}
			if job.Cluster == "" {
				return nil, fmt.Errorf("job %q needs an explicit configured fallback cluster before enabling affinity", job.Name)
			}
			determined, movable, err := config.DetermineClusterForJob(job, file.path, inventory)
			if err != nil {
				return nil, err
			}
			if !movable && string(determined) != job.Cluster {
				return nil, fmt.Errorf("job %q configured fallback %q differs from fixed placement %q", job.Name, job.Cluster, determined)
			}
			if old, exists := previous[job.Name]; exists && old.Cluster != job.Cluster {
				return nil, fmt.Errorf("job %q configured fallback %q differs from preserved baseline %q; reconcile routing before enabling affinity", job.Name, job.Cluster, old.Cluster)
			}
			current[job.Name] = dispatcher.ProwJobData{
				Cluster: job.Cluster, Group: group, Capabilities: extractCapabilities(job.Labels),
				PlacementKnown: true, Relocatable: movable,
				PlacementDigest: placementDigest(job, string(determined), movable, group, config),
				Demand:          demandForJob(nil, job.Name, minimum),
			}
			if old, exists := previous[job.Name]; exists && old.Demand > 0 {
				value := current[job.Name]
				value.Demand = old.Demand
				current[job.Name] = value
			}
		}
	}
	if err := validateAffinitySnapshot(snapshotPath, current); err != nil {
		return nil, err
	}
	bindings, err := store.ResolveAll(current, inventory)
	if err != nil {
		return nil, err
	}
	for name, binding := range bindings {
		job := current[name]
		if binding.Cluster != job.Cluster {
			return nil, fmt.Errorf("job %q fallback %q conflicts with retained affinity %q", name, job.Cluster, binding.Cluster)
		}
		job.Affinity = binding
		current[name] = job
	}
	bound, err := store.Commit(current, inventory)
	if err != nil {
		return nil, fmt.Errorf("preserving configured fallback affinity: %w", err)
	}
	return bound, nil
}

// validateAffinitySnapshot refuses migration from a divergent runtime assignment.
// It accepts previous compiler formats only as evidence of old routing, never as
// a valid snapshot to serve. Existing warm hosts need reconciliation by operators.
func validateAffinitySnapshot(path string, current map[string]dispatcher.ProwJobData) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read affinity bootstrap snapshot: %w", err)
	}
	var snapshot dispatcher.PolicySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode affinity bootstrap snapshot: %w", err)
	}
	if snapshot.Assignments == nil {
		return errors.New("affinity bootstrap snapshot has no assignments")
	}
	for name, job := range current {
		if assignment, exists := snapshot.Assignments[name]; exists && assignment.Cluster != job.Cluster {
			return fmt.Errorf("job %q cached runtime host %q differs from fallback %q; reconcile routing before enabling affinity", name, assignment.Cluster, job.Cluster)
		}
	}
	return nil
}
