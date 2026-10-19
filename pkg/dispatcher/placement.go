package dispatcher

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// placementUnit keeps jobs sharing a configuration and baseline cluster together.
// Its aggregate job intersects eligibility by requiring every member's capabilities
// and preserving any member's placement restriction.
type placementUnit struct {
	key   string
	names []string
	job   ProwJobData
}

// overridePlacementUnits rejects scopes that would split a colocation unit.
func overridePlacementUnits(override *dispatcherv1.DispatchOverride, baseline map[string]ProwJobData) ([]placementUnit, error) {
	units := make(map[string]*placementUnit)
	for _, name := range sortedProwJobNames(baseline) {
		job := baseline[name]
		if job.Cluster != override.Spec.Cluster {
			continue
		}
		key := "group:" + job.Group + "\x00" + job.Cluster
		if job.Group == "" {
			key = "job:" + name + "\x00" + job.Cluster
		}
		unit := units[key]
		if unit == nil {
			unit = &placementUnit{key: key, job: ProwJobData{Cluster: job.Cluster, Group: job.Group, PlacementKnown: true, Relocatable: true}}
			units[key] = unit
		}
		unit.names = append(unit.names, name)
		unit.job.Demand += effectiveDemand(job.Demand)
		unit.job.PlacementKnown = unit.job.PlacementKnown && job.PlacementKnown
		unit.job.Relocatable = unit.job.Relocatable && job.Relocatable && job.Affinity == (PlacementAffinity{})
		unit.job.Capabilities = append(unit.job.Capabilities, job.Capabilities...)
	}
	result := make([]placementUnit, 0, len(units))
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		unit := units[key]
		matched := 0
		for _, name := range unit.names {
			if jobHasCapability(baseline[name], override.Spec.Scope.Capability) {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		if matched != len(unit.names) {
			return nil, fmt.Errorf("override %q capability %q splits colocation group %q on cluster %q", override.Spec.ID, override.Spec.Scope.Capability, unit.job.Group, unit.job.Cluster)
		}
		unit.job.Capabilities = sets.List(sets.New[string](unit.job.Capabilities...))
		result = append(result, *unit)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].job.Demand != result[j].job.Demand {
			return result[i].job.Demand > result[j].job.Demand
		}
		return result[i].key < result[j].key
	})
	return result, nil
}

// overrideCapacities isolates capability-scoped reductions from unrelated work.
func overrideCapacities(override *dispatcherv1.DispatchOverride, capacities map[string]int) map[string]int {
	if override.Spec.Scope.Capability == "" {
		return capacities
	}
	result := make(map[string]int, len(capacities))
	for cluster, capacity := range capacities {
		result[cluster] = capacity
	}
	result[override.Spec.Cluster] = 0
	if override.Spec.Kind == dispatcherv1.OverrideKindCapacity {
		result[override.Spec.Cluster] = int(*override.Spec.Capacity)
	}
	return result
}
