package api

import v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ProductLifecyclePhases holds the lifecycle phases for all versions of all products.
type ProductLifecyclePhases struct {
	// ByProduct maps product name to the lifecycle phases for all versions
	ByProduct map[string]VersionLifecyclePhases `json:"by_product"`
}

// VersionLifecyclePhases holds the lifecycle phases for all versions of a product.
type VersionLifecyclePhases struct {
	// ByVersion maps version to the lifecycle phases
	ByVersion map[string]LifecyclePhases `json:"by_version"`
}

// LifecyclePhases holds the lifecycle phases for a version of a product.
type LifecyclePhases struct {
	// Previous lists all lifecycle phases that have already occurred. Optional.
	Previous []LifecyclePhase `json:"previous,omitempty"`
	// Next holds the phase which will occur next
	Next LifecyclePhase `json:"next"`
}

// LifecyclePhase describes a phase in the release lifecycle for a version of a product.
type LifecyclePhase struct {
	// Event is the name of this phase.
	Event LifecycleEvent `json:"event"`
	// When is the moment in time when this phase begins. Optional.
	When *v1.Time `json:"when,omitempty"`
}

// LifecycleEvent is an event in the lifecycle of a version of a product.
type LifecycleEvent string

const (
	// LifecycleEventOpen marks the moment that development branches open for changes.
	LifecycleEventOpen LifecycleEvent = "open"
	// LifecycleEventFeatureFreeze marks the moment that development branches close for new features.
	// At this point it is expected that only stabilizing bug-fixes land in the branches.
	LifecycleEventFeatureFreeze LifecycleEvent = "feature-freeze"
	// LifecycleEventCodeFreeze marks the moment that development branches close for contribution.
	// At this point it is expected that only urgent stabilizing bug-fixes land in the branches.
	LifecycleEventCodeFreeze LifecycleEvent = "code-freeze"
	// LifecycleEventGenerallyAvailable marks the moment that a version is available and the development
	// branches begin to track the next release.
	LifecycleEventGenerallyAvailable LifecycleEvent = "generally-available"
	// LifecycleEventEndOfLife marks the moment that a version is no longer supported and release branches
	// close for good for this version.
	LifecycleEventEndOfLife LifecycleEvent = "end-of-life"
)
