package main

import (
	"fmt"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

// jobVolumeSource supplies demand and capacity shares for a full generation.
type jobVolumeSource interface {
	GetJobVolumes() (map[string]float64, error)
	CalculateVolumeDistribution(dispatcher.ClusterMap) map[string]float64
}

// ephemeralResetter initializes the nonpersistent ephemeral scheduler.
type ephemeralResetter interface {
	Reset([]string)
}

// fullDispatchRunner reconciles durable assignments and their serving snapshot.
type fullDispatchRunner struct {
	options         options
	jobs            *dispatcher.Prowjobs
	volumes         jobVolumeSource
	ephemeral       ephemeralResetter
	publishSnapshot func(map[string]dispatcher.ProwJobData, dispatcher.ClusterMap, sets.Set[string]) error
	slack           slackClient
}

// dispatch publishes current assignments or generates replacements when required.
func (runner *fullDispatchRunner) dispatch(forceDispatch bool) error {
	config, err := dispatcher.LoadConfig(runner.options.configPath)
	if err != nil {
		return fmt.Errorf("failed to load config from %q: %w", runner.options.configPath, err)
	}

	configClusterMap, blocked, err := dispatcher.LoadClusterConfig(runner.options.clusterConfigPath)
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

	newBlockedClusters := runner.jobs.HasAnyOfClusters(blocked)
	currentAssignments := runner.jobs.GetDataCopy()
	missingAssignments := len(currentAssignments) == 0
	aggregateJobs, aggregateErr := loadAggregateJobs(runner.options.releaseConfigDir)
	for name := range aggregateJobs {
		if _, found := currentAssignments[name]; !found {
			missingAssignments = true
		}
	}
	metadataMigrationRequired := assignmentsRequireMetadataMigration(currentAssignments)

	if !forceDispatch && !inventoryChanged && !newBlockedClusters && !missingAssignments && !metadataMigrationRequired {
		// The ephemeral scheduler is not persisted in the Gob cache, so it still
		// needs initialization after a restart when no generation is necessary.
		runner.ephemeral.Reset(sets.List(clustersFromConfig))
		return utilerrors.NewAggregate([]error{aggregateErr, runner.publishSnapshot(currentAssignments, configClusterMap, blocked)})
	}
	// Invalid aliases must not prevent serving a known-good baseline, but cannot
	// be used to generate replacement assignments.
	if aggregateErr != nil {
		return aggregateErr
	}

	jobVolumes, err := runner.volumes.GetJobVolumes()
	if err != nil {
		return fmt.Errorf("failed to get job volumes: %w", err)
	}

	pjs, err := dispatchJobsWithPolicy(
		runner.options.prowJobConfigDir, config, jobVolumes, blocked, runner.volumes.CalculateVolumeDistribution(configClusterMap), configClusterMap,
		currentAssignments, runner.options.rebalanceHysteresisPercent, runner.options.movementBudgetPercent, runner.options.MinimumJobDemand, runner.options.releaseConfigDir,
	)
	if err != nil {
		return fmt.Errorf("failed to dispatch jobs: %w", err)
	}

	markerPath := fallbackPublicationMarkerPath(runner.options.jobsStoragePath)
	if runner.options.createPR {
		if err := markFallbackPublicationPending(markerPath); err != nil {
			return err
		}
	}

	var committedWriteErr error
	if err := writeFullDispatchAssignments(runner.options.jobsStoragePath, pjs, dispatcher.WriteGob); err != nil {
		if !dispatcher.IsGobWriteCommitted(err) {
			return fmt.Errorf("failed to persist job assignments: %w", err)
		}
		committedWriteErr = fmt.Errorf("job assignments were replaced but their directory sync failed: %w", err)
	}
	runner.jobs.Regenerate(pjs)
	runner.ephemeral.Reset(sets.List(clustersFromConfig))
	if err := runner.publishSnapshot(pjs, configClusterMap, blocked); err != nil {
		return utilerrors.NewAggregate([]error{committedWriteErr, fmt.Errorf("failed to publish policy snapshot: %w", err)})
	}
	if committedWriteErr == nil {
		if err := writePublishedInventoryDigest(publishedInventoryDigestPath(runner.options.jobsStoragePath), inventoryDigest); err != nil {
			return fmt.Errorf("failed to publish cluster inventory digest: %w", err)
		}
	}

	if runner.options.createPR {
		if err := createPR(runner.options, config, pjs, configClusterMap); err != nil {
			return utilerrors.NewAggregate([]error{committedWriteErr, err})
		}
		if err := clearFallbackPublicationPending(markerPath); err != nil {
			return utilerrors.NewAggregate([]error{committedWriteErr, err})
		}
		if err := sendSlackMessage(runner.slack, runner.options.opsChannelId); err != nil {
			logrus.WithError(err).Error("Failed to post message in ops channel")
		}
	}
	return committedWriteErr
}
