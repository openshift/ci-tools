package dispatcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

const (
	// SnapshotFormatVersion is the persisted snapshot schema version.
	SnapshotFormatVersion = 1
	// SnapshotCompilerVersion identifies placement semantics included in the input digest.
	SnapshotCompilerVersion = "dispatcher-future-v1"
)

// SnapshotAssignment is one immutable scheduling decision in a PolicySnapshot.
type SnapshotAssignment struct {
	Cluster         string    `json:"cluster"`
	BaselineCluster string    `json:"baselineCluster"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	Demand          float64   `json:"demand"`
	OverrideID      string    `json:"overrideID,omitempty"`
	ValidUntil      time.Time `json:"validUntil,omitempty"`
	Explanation     string    `json:"explanation,omitempty"`
}

// PolicySnapshot is a complete, immutable dispatcher policy generation.
type PolicySnapshot struct {
	FormatVersion   int                           `json:"formatVersion"`
	CompilerVersion string                        `json:"compilerVersion"`
	Generation      uint64                        `json:"generation"`
	GeneratedAt     time.Time                     `json:"generatedAt"`
	InputDigest     string                        `json:"inputDigest"`
	BaselineDigest  string                        `json:"baselineDigest"`
	InventoryDigest string                        `json:"inventoryDigest"`
	OverridesDigest string                        `json:"overridesDigest"`
	Checksum        string                        `json:"checksum"`
	Assignments     map[string]SnapshotAssignment `json:"assignments"`
	Baseline        map[string]ProwJobData        `json:"baseline"`
	Inventory       ClusterMap                    `json:"inventory"`
	Blocked         []string                      `json:"blocked,omitempty"`
	OverrideIDs     []string                      `json:"overrideIDs,omitempty"`
}

// Decision explains the cluster selected from a snapshot.
type Decision struct {
	Cluster          string    `json:"cluster"`
	Source           string    `json:"source"`
	PolicyGeneration uint64    `json:"policyGeneration"`
	PolicyDigest     string    `json:"policyDigest"`
	OverrideID       string    `json:"overrideID,omitempty"`
	ValidUntil       time.Time `json:"validUntil,omitempty"`
	Explanation      string    `json:"explanation"`
}

// CompileInput contains every policy input used to build a snapshot.
type CompileInput struct {
	Baseline   map[string]ProwJobData
	Inventory  ClusterMap
	Blocked    sets.Set[string]
	Overrides  []dispatcherv1.DispatchOverride
	Generation uint64
	Now        time.Time
}

// CompileSummary describes the impact of active overrides.
type CompileSummary struct {
	AffectedJobs   int                       `json:"affectedJobs"`
	AffectedGroups int                       `json:"affectedGroups"`
	AffectedDemand float64                   `json:"affectedDemand"`
	MovableJobs    int                       `json:"movableJobs"`
	MovableDemand  float64                   `json:"movableDemand"`
	MovedJobs      int                       `json:"movedJobs"`
	MovedDemand    float64                   `json:"movedDemand"`
	ImmovableJobs  []string                  `json:"immovableJobs,omitempty"`
	Destinations   map[string]float64        `json:"destinations,omitempty"`
	ByOverride     map[string]CompileSummary `json:"-"`
}

type canonicalOverride struct {
	ID                string                             `json:"id"`
	Kind              dispatcherv1.OverrideKind          `json:"kind"`
	Cluster           string                             `json:"cluster"`
	Scope             dispatcherv1.DispatchOverrideScope `json:"scope"`
	Capacity          *int32                             `json:"capacity,omitempty"`
	StartsAt          time.Time                          `json:"startsAt"`
	ExpiresAt         time.Time                          `json:"expiresAt"`
	Approvals         []string                           `json:"approvals,omitempty"`
	RequiredApprovals int32                              `json:"requiredApprovals"`
	RevokedAt         *time.Time                         `json:"revokedAt,omitempty"`
}

func digestJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalOverrides(overrides []dispatcherv1.DispatchOverride) []canonicalOverride {
	result := make([]canonicalOverride, 0, len(overrides))
	for i := range overrides {
		approvalSet := sets.New[string]()
		for _, approval := range overrides[i].Spec.Approvals {
			approvalSet.Insert(approval.UserID)
		}
		item := canonicalOverride{
			ID:                overrides[i].Spec.ID,
			Kind:              overrides[i].Spec.Kind,
			Cluster:           overrides[i].Spec.Cluster,
			Scope:             overrides[i].Spec.Scope,
			Capacity:          overrides[i].Spec.Capacity,
			StartsAt:          overrides[i].Spec.StartsAt.Time.UTC(),
			ExpiresAt:         overrides[i].Spec.ExpiresAt.Time.UTC(),
			Approvals:         sets.List(approvalSet),
			RequiredApprovals: overrides[i].Spec.RequiredApprovals,
		}
		if overrides[i].Spec.RevokedAt != nil {
			revokedAt := overrides[i].Spec.RevokedAt.Time.UTC()
			item.RevokedAt = &revokedAt
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func snapshotChecksum(snapshot *PolicySnapshot) (string, error) {
	copy := *snapshot
	copy.Checksum = ""
	return digestJSON(copy)
}

func copyBaseline(input map[string]ProwJobData) map[string]ProwJobData {
	result := make(map[string]ProwJobData, len(input))
	for name, data := range input {
		data.Capabilities = append([]string(nil), data.Capabilities...)
		sort.Strings(data.Capabilities)
		result[name] = data
	}
	return result
}

func sortedProwJobNames(input map[string]ProwJobData) []string {
	names := make([]string, 0, len(input))
	for name := range input {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func copyInventory(input ClusterMap) ClusterMap {
	result := make(ClusterMap, len(input))
	for name, info := range input {
		info.Capabilities = append([]string(nil), info.Capabilities...)
		sort.Strings(info.Capabilities)
		result[name] = info
	}
	return result
}

func effectiveDemand(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 1
	}
	return value
}

func uniqueApprovals(override *dispatcherv1.DispatchOverride) int {
	users := sets.New[string]()
	for _, approval := range override.Spec.Approvals {
		if approval.UserID != "" {
			users.Insert(approval.UserID)
		}
	}
	return users.Len()
}

// OverrideIsActive reports whether an override is approved and effective at now.
func OverrideIsActive(override *dispatcherv1.DispatchOverride, now time.Time) bool {
	return override.Spec.RevokedAt == nil &&
		!now.Before(override.Spec.StartsAt.Time) && now.Before(override.Spec.ExpiresAt.Time) &&
		uniqueApprovals(override) >= int(override.Spec.RequiredApprovals)
}

func scopesOverlap(_, _ dispatcherv1.DispatchOverrideScope) bool {
	// A job may require more than one capability, so differently named capability
	// scopes on one cluster are not provably disjoint. Keep first-release policy
	// conservative and allow only one live override per cluster.
	return true
}

// ValidateOverrideSet rejects ambiguous overlapping runtime policy.
func ValidateOverrideSet(overrides []dispatcherv1.DispatchOverride, now time.Time) error {
	active := make([]dispatcherv1.DispatchOverride, 0, len(overrides))
	for i := range overrides {
		if overrides[i].Spec.RevokedAt == nil && now.Before(overrides[i].Spec.ExpiresAt.Time) {
			active = append(active, overrides[i])
		}
	}
	for i := range active {
		for j := i + 1; j < len(active); j++ {
			if active[i].Spec.Cluster == active[j].Spec.Cluster && scopesOverlap(active[i].Spec.Scope, active[j].Spec.Scope) {
				return fmt.Errorf("overrides %q and %q overlap on cluster %q", active[i].Spec.ID, active[j].Spec.ID, active[i].Spec.Cluster)
			}
		}
	}
	return nil
}

func validateOverridePlacementPools(overrides []dispatcherv1.DispatchOverride, inventory ClusterMap, now time.Time) error {
	live := make([]dispatcherv1.DispatchOverride, 0, len(overrides))
	for i := range overrides {
		if overrides[i].Spec.RevokedAt == nil && now.Before(overrides[i].Spec.ExpiresAt.Time) {
			live = append(live, overrides[i])
		}
	}
	for i := range live {
		left, leftExists := inventory[live[i].Spec.Cluster]
		if !leftExists {
			continue
		}
		for j := i + 1; j < len(live); j++ {
			right, rightExists := inventory[live[j].Spec.Cluster]
			if rightExists && left.Provider == right.Provider {
				return fmt.Errorf("overrides %q and %q share provider placement pool %q", live[i].Spec.ID, live[j].Spec.ID, left.Provider)
			}
		}
	}
	return nil
}

func jobHasCapability(job ProwJobData, capability string) bool {
	if capability == "" {
		return true
	}
	return sets.New[string](job.Capabilities...).Has(capability)
}

func eligibleDestination(job ProwJobData, source string, candidate string, inventory ClusterMap, capacities map[string]int, blocked sets.Set[string]) bool {
	if candidate == source || blocked.Has(candidate) || capacities[candidate] <= 0 {
		return false
	}
	sourceInfo, sourceExists := inventory[source]
	candidateInfo, candidateExists := inventory[candidate]
	if !sourceExists || !candidateExists || sourceInfo.Provider != candidateInfo.Provider {
		return false
	}
	return matchesAllCapabilities(candidateInfo.Capabilities, job.Capabilities)
}

func placementTieBreak(job, cluster string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(job))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cluster))
	return h.Sum64()
}

func chooseDestination(jobName string, job ProwJobData, source string, inventory ClusterMap, capacities map[string]int, blocked sets.Set[string], loads map[string]float64) string {
	clusters := make([]string, 0, len(inventory))
	for cluster := range inventory {
		clusters = append(clusters, cluster)
	}
	sort.Strings(clusters)
	selected := ""
	minScore := 0.0
	var minTie uint64
	for _, candidate := range clusters {
		if !eligibleDestination(job, source, candidate, inventory, capacities, blocked) {
			continue
		}
		score := (loads[candidate] + effectiveDemand(job.Demand)) / float64(capacities[candidate])
		tie := placementTieBreak(jobName, candidate)
		if selected == "" || score < minScore || score == minScore && (tie < minTie || tie == minTie && candidate < selected) {
			selected, minScore, minTie = candidate, score, tie
		}
	}
	return selected
}

func validateOverride(override *dispatcherv1.DispatchOverride, baseline map[string]ProwJobData, inventory ClusterMap, blocked sets.Set[string]) error {
	if override.Spec.Kind != dispatcherv1.OverrideKindCapacity && override.Spec.Kind != dispatcherv1.OverrideKindDrain {
		return fmt.Errorf("override %q has unsupported kind %q", override.Spec.ID, override.Spec.Kind)
	}
	if override.Spec.Kind == dispatcherv1.OverrideKindCapacity {
		if override.Spec.Capacity == nil || *override.Spec.Capacity < 1 || *override.Spec.Capacity > 100 {
			return fmt.Errorf("override %q capacity must be between 1 and 100", override.Spec.ID)
		}
	} else if override.Spec.Capacity != nil {
		return fmt.Errorf("drain override %q must not set capacity", override.Spec.ID)
	}
	info, exists := inventory[override.Spec.Cluster]
	if !exists {
		if blocked.Has(override.Spec.Cluster) {
			return nil
		}
		return fmt.Errorf("override %q targets unknown or inactive cluster %q", override.Spec.ID, override.Spec.Cluster)
	}
	if override.Spec.Kind == dispatcherv1.OverrideKindCapacity {
		if int(*override.Spec.Capacity) >= info.Capacity {
			return fmt.Errorf("override %q capacity %d must lower cluster %q baseline capacity %d", override.Spec.ID, *override.Spec.Capacity, override.Spec.Cluster, info.Capacity)
		}
	}
	capability := override.Spec.Scope.Capability
	if capability == "" {
		return nil
	}
	if !sets.New[string](info.Capabilities...).Has(capability) {
		return fmt.Errorf("override %q capability %q is not represented by cluster %q", override.Spec.ID, capability, override.Spec.Cluster)
	}
	for _, jobName := range sortedProwJobNames(baseline) {
		job := baseline[jobName]
		if job.Cluster == override.Spec.Cluster && jobHasCapability(job, capability) {
			return nil
		}
	}
	return fmt.Errorf("override %q capability %q has no classified workload on cluster %q", override.Spec.ID, capability, override.Spec.Cluster)
}

func overrideJobs(override *dispatcherv1.DispatchOverride, baseline map[string]ProwJobData) []string {
	jobs := make([]string, 0)
	for _, name := range sortedProwJobNames(baseline) {
		job := baseline[name]
		if job.Cluster == override.Spec.Cluster && jobHasCapability(job, override.Spec.Scope.Capability) {
			jobs = append(jobs, name)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		iDemand := effectiveDemand(baseline[jobs[i]].Demand)
		jDemand := effectiveDemand(baseline[jobs[j]].Demand)
		if iDemand != jDemand {
			return iDemand > jDemand
		}
		return jobs[i] < jobs[j]
	})
	return jobs
}

func targetLoadForCapacity(override *dispatcherv1.DispatchOverride, baseline map[string]ProwJobData, inventory ClusterMap, capacities map[string]int) float64 {
	source := override.Spec.Cluster
	sourceInfo := inventory[source]
	totalDemand, totalCapacity := 0.0, 0
	for _, jobName := range sortedProwJobNames(baseline) {
		job := baseline[jobName]
		info, exists := inventory[job.Cluster]
		if !exists || info.Provider != sourceInfo.Provider || !jobHasCapability(job, override.Spec.Scope.Capability) {
			continue
		}
		totalDemand += effectiveDemand(job.Demand)
	}
	for cluster, info := range inventory {
		if info.Provider == sourceInfo.Provider && matchesAllCapabilities(info.Capabilities, capabilityList(override.Spec.Scope.Capability)) {
			capacity := capacities[cluster]
			if cluster == source && override.Spec.Scope.Capability != "" {
				capacity = int(*override.Spec.Capacity)
			}
			totalCapacity += capacity
		}
	}
	if totalCapacity == 0 {
		return 0
	}
	sourceCapacity := capacities[source]
	if override.Spec.Scope.Capability != "" {
		sourceCapacity = int(*override.Spec.Capacity)
	}
	return totalDemand * float64(sourceCapacity) / float64(totalCapacity)
}

func capabilityList(capability string) []string {
	if capability == "" {
		return nil
	}
	return []string{capability}
}

// CompileSnapshot produces a complete immutable snapshot and an override impact summary.
func CompileSnapshot(input CompileInput) (*PolicySnapshot, CompileSummary, error) {
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	if input.Generation == 0 {
		input.Generation = 1
	}
	baseline := copyBaseline(input.Baseline)
	inventory := copyInventory(input.Inventory)
	blocked := input.Blocked.Clone()
	if blocked == nil {
		blocked = sets.New[string]()
	}
	if err := ValidateOverrideSet(input.Overrides, input.Now); err != nil {
		return nil, CompileSummary{}, err
	}
	if err := validateOverridePlacementPools(input.Overrides, inventory, input.Now); err != nil {
		return nil, CompileSummary{}, err
	}

	active := make([]dispatcherv1.DispatchOverride, 0, len(input.Overrides))
	for i := range input.Overrides {
		if !OverrideIsActive(&input.Overrides[i], input.Now) {
			continue
		}
		if err := validateOverride(&input.Overrides[i], baseline, inventory, blocked); err != nil {
			return nil, CompileSummary{}, err
		}
		active = append(active, input.Overrides[i])
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Spec.ID < active[j].Spec.ID })

	assignments := make(map[string]SnapshotAssignment, len(baseline))
	loads := make(map[string]float64, len(inventory))
	for _, name := range sortedProwJobNames(baseline) {
		job := baseline[name]
		demand := effectiveDemand(job.Demand)
		assignments[name] = SnapshotAssignment{Cluster: job.Cluster, BaselineCluster: job.Cluster, Capabilities: append([]string(nil), job.Capabilities...), Demand: demand, Explanation: "baseline Git assignment"}
		loads[job.Cluster] += demand
	}
	capacities := make(map[string]int, len(inventory))
	for cluster, info := range inventory {
		capacities[cluster] = info.Capacity
	}
	for _, override := range active {
		if override.Spec.Kind == dispatcherv1.OverrideKindDrain && override.Spec.Scope.Capability == "" {
			capacities[override.Spec.Cluster] = 0
		} else if override.Spec.Kind == dispatcherv1.OverrideKindCapacity && override.Spec.Scope.Capability == "" {
			capacities[override.Spec.Cluster] = int(*override.Spec.Capacity)
		}
	}

	summary := CompileSummary{Destinations: make(map[string]float64), ByOverride: make(map[string]CompileSummary)}
	affectedGroups := sets.New[string]()
	for i := range active {
		override := &active[i]
		overrideSummary := CompileSummary{Destinations: make(map[string]float64)}
		overrideAffectedGroups := sets.New[string]()
		jobs := overrideJobs(override, baseline)
		overrideImmovableJobs := 0
		sourceAffectedLoad := 0.0
		for _, jobName := range jobs {
			sourceAffectedLoad += effectiveDemand(baseline[jobName].Demand)
		}
		targetLoad := 0.0
		if override.Spec.Kind == dispatcherv1.OverrideKindCapacity {
			targetLoad = targetLoadForCapacity(override, baseline, inventory, capacities)
		}
		for _, jobName := range jobs {
			job := baseline[jobName]
			demand := effectiveDemand(job.Demand)
			summary.AffectedJobs++
			overrideSummary.AffectedJobs++
			group := job.Group
			if group == "" {
				group = jobName
			}
			affectedGroups.Insert(group)
			overrideAffectedGroups.Insert(group)
			summary.AffectedDemand += demand
			overrideSummary.AffectedDemand += demand
			candidateCapacities := capacities
			if override.Spec.Scope.Capability != "" {
				candidateCapacities = make(map[string]int, len(capacities))
				for cluster, capacity := range capacities {
					candidateCapacities[cluster] = capacity
				}
				if override.Spec.Kind == dispatcherv1.OverrideKindDrain {
					candidateCapacities[override.Spec.Cluster] = 0
				} else {
					candidateCapacities[override.Spec.Cluster] = int(*override.Spec.Capacity)
				}
			}
			destination := chooseDestination(jobName, job, override.Spec.Cluster, inventory, candidateCapacities, blocked, loads)
			if destination == "" {
				summary.ImmovableJobs = append(summary.ImmovableJobs, jobName)
				overrideSummary.ImmovableJobs = append(overrideSummary.ImmovableJobs, jobName)
				overrideImmovableJobs++
				continue
			}
			summary.MovableJobs++
			summary.MovableDemand += demand
			overrideSummary.MovableJobs++
			overrideSummary.MovableDemand += demand
			if override.Spec.Kind == dispatcherv1.OverrideKindCapacity && sourceAffectedLoad <= targetLoad {
				continue
			}
			if override.Spec.Kind == dispatcherv1.OverrideKindCapacity &&
				math.Abs(sourceAffectedLoad-targetLoad) <= math.Abs(sourceAffectedLoad-demand-targetLoad) {
				continue
			}
			assignment := assignments[jobName]
			assignment.Cluster = destination
			assignment.OverrideID = override.Spec.ID
			assignment.ValidUntil = override.Spec.ExpiresAt.Time.UTC()
			assignment.Explanation = fmt.Sprintf("%s override %s moved workload from %s", strings.ToLower(string(override.Spec.Kind)), override.Spec.ID, override.Spec.Cluster)
			assignments[jobName] = assignment
			loads[override.Spec.Cluster] -= demand
			loads[destination] += demand
			sourceAffectedLoad -= demand
			summary.MovedJobs++
			summary.MovedDemand += demand
			summary.Destinations[destination] += demand
			overrideSummary.MovedJobs++
			overrideSummary.MovedDemand += demand
			overrideSummary.Destinations[destination] += demand
		}
		if override.Spec.Kind == dispatcherv1.OverrideKindDrain && overrideImmovableJobs > 0 {
			return nil, summary, fmt.Errorf("drain %q has no eligible destination for %d affected workloads", override.Spec.ID, overrideImmovableJobs)
		}
		sort.Strings(overrideSummary.ImmovableJobs)
		overrideSummary.AffectedGroups = overrideAffectedGroups.Len()
		summary.ByOverride[override.Spec.ID] = overrideSummary
	}
	sort.Strings(summary.ImmovableJobs)
	summary.AffectedGroups = affectedGroups.Len()

	baselineDigest, err := digestJSON(baseline)
	if err != nil {
		return nil, summary, fmt.Errorf("digest baseline: %w", err)
	}
	inventoryDigest, err := digestJSON(struct {
		Inventory ClusterMap `json:"inventory"`
		Blocked   []string   `json:"blocked"`
	}{Inventory: inventory, Blocked: sets.List(blocked)})
	if err != nil {
		return nil, summary, fmt.Errorf("digest inventory: %w", err)
	}
	// Only effective policy belongs in the serving digest. This deliberately
	// changes when an override becomes active, expires, or is revoked even when
	// the durable object itself has not changed at that instant.
	overridesDigest, err := digestJSON(canonicalOverrides(active))
	if err != nil {
		return nil, summary, fmt.Errorf("digest overrides: %w", err)
	}
	inputDigest, err := digestJSON(struct {
		Compiler  string `json:"compiler"`
		Baseline  string `json:"baseline"`
		Inventory string `json:"inventory"`
		Overrides string `json:"overrides"`
	}{SnapshotCompilerVersion, baselineDigest, inventoryDigest, overridesDigest})
	if err != nil {
		return nil, summary, fmt.Errorf("digest policy input: %w", err)
	}
	overrideIDs := make([]string, 0, len(active))
	for i := range active {
		overrideIDs = append(overrideIDs, active[i].Spec.ID)
	}
	snapshot := &PolicySnapshot{
		FormatVersion: SnapshotFormatVersion, CompilerVersion: SnapshotCompilerVersion,
		Generation: input.Generation, GeneratedAt: input.Now.UTC(), InputDigest: inputDigest,
		BaselineDigest: baselineDigest, InventoryDigest: inventoryDigest, OverridesDigest: overridesDigest,
		Assignments: assignments, Baseline: baseline, Inventory: inventory, Blocked: sets.List(blocked), OverrideIDs: overrideIDs,
	}
	snapshot.Checksum, err = snapshotChecksum(snapshot)
	if err != nil {
		return nil, summary, fmt.Errorf("checksum snapshot: %w", err)
	}
	return snapshot, summary, nil
}

// SnapshotManager atomically publishes and serves immutable policy snapshots.
type SnapshotManager struct {
	path     string
	current  atomic.Pointer[PolicySnapshot]
	verified atomic.Bool
}

// NewSnapshotManager creates a snapshot manager using path as an optional restart cache.
func NewSnapshotManager(path string) *SnapshotManager {
	return &SnapshotManager{path: path}
}

// Ready reports whether a valid snapshot is currently loaded.
func (m *SnapshotManager) Ready() bool { return m.verified.Load() && m.current.Load() != nil }

// Current returns a defensive copy of the current snapshot.
func (m *SnapshotManager) Current() *PolicySnapshot {
	current := m.current.Load()
	if current == nil {
		return nil
	}
	copy := *current
	copy.Assignments = make(map[string]SnapshotAssignment, len(current.Assignments))
	for name, assignment := range current.Assignments {
		assignment.Capabilities = append([]string(nil), assignment.Capabilities...)
		copy.Assignments[name] = assignment
	}
	copy.Baseline = make(map[string]ProwJobData, len(current.Baseline))
	for name, job := range current.Baseline {
		job.Capabilities = append([]string(nil), job.Capabilities...)
		copy.Baseline[name] = job
	}
	copy.Inventory = make(ClusterMap, len(current.Inventory))
	for cluster, info := range current.Inventory {
		info.Capabilities = append([]string(nil), info.Capabilities...)
		copy.Inventory[cluster] = info
	}
	copy.Blocked = append([]string(nil), current.Blocked...)
	copy.OverrideIDs = append([]string(nil), current.OverrideIDs...)
	return &copy
}

// Publish validates, durably caches, and atomically installs a snapshot.
func (m *SnapshotManager) Publish(snapshot *PolicySnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}
	checksum, err := snapshotChecksum(snapshot)
	if err != nil {
		return err
	}
	if snapshot.Checksum != checksum {
		return fmt.Errorf("snapshot checksum mismatch: got %q, expected %q", snapshot.Checksum, checksum)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("copy snapshot: %w", err)
	}
	var installed PolicySnapshot
	if err := json.Unmarshal(data, &installed); err != nil {
		return fmt.Errorf("copy snapshot: %w", err)
	}
	if m.path != "" {
		if err := writeSnapshot(m.path, &installed); err != nil {
			return err
		}
	}
	m.current.Store(&installed)
	m.verified.Store(true)
	observeSnapshot(&installed)
	return nil
}

// Load restores and validates the local snapshot restart cache.
func (m *SnapshotManager) Load() error {
	if m.path == "" {
		return errors.New("snapshot cache path is empty")
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	var snapshot PolicySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	if snapshot.FormatVersion != SnapshotFormatVersion || snapshot.CompilerVersion != SnapshotCompilerVersion {
		return fmt.Errorf("unsupported snapshot format %d compiler %q", snapshot.FormatVersion, snapshot.CompilerVersion)
	}
	checksum, err := snapshotChecksum(&snapshot)
	if err != nil {
		return err
	}
	if checksum != snapshot.Checksum {
		return fmt.Errorf("snapshot checksum mismatch: got %q, expected %q", snapshot.Checksum, checksum)
	}
	m.verified.Store(false)
	m.current.Store(&snapshot)
	observeSnapshot(nil)
	return nil
}

func writeSnapshot(path string, snapshot *PolicySnapshot) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".dispatcher-snapshot-*")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(snapshot); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open snapshot directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync snapshot directory: %w", err)
	}
	return nil
}

// Lookup returns the current decision for job. Expired runtime policy fails back
// to the Git-materialized baseline even if a cleanup controller is unavailable.
func (m *SnapshotManager) Lookup(job string, now time.Time) (Decision, bool) {
	if !m.Ready() {
		return Decision{}, false
	}
	snapshot := m.current.Load()
	if snapshot == nil {
		return Decision{}, false
	}
	assignment, exists := snapshot.Assignments[job]
	if !exists {
		return Decision{}, false
	}
	decision := Decision{
		Cluster: assignment.Cluster, Source: "baseline", PolicyGeneration: snapshot.Generation,
		PolicyDigest: snapshot.InputDigest, OverrideID: assignment.OverrideID,
		ValidUntil: assignment.ValidUntil, Explanation: assignment.Explanation,
	}
	if assignment.OverrideID != "" {
		decision.Source = "runtime-override"
		if !assignment.ValidUntil.IsZero() && !now.Before(assignment.ValidUntil) {
			decision.Cluster = assignment.BaselineCluster
			decision.Source = "expired-override-fallback"
			decision.OverrideID = ""
			decision.ValidUntil = time.Time{}
			decision.Explanation = "runtime override expired; using Git baseline"
		}
	}
	return decision, true
}

// NewOverride constructs an override with Kubernetes timestamps for callers that
// do not otherwise need to import metav1.
func NewOverride(spec dispatcherv1.DispatchOverrideSpec) dispatcherv1.DispatchOverride {
	return dispatcherv1.DispatchOverride{
		TypeMeta:   metav1.TypeMeta{APIVersion: dispatcherv1.SchemeGroupVersion.String(), Kind: "DispatchOverride"},
		ObjectMeta: metav1.ObjectMeta{Name: spec.ID}, Spec: spec,
	}
}
