package main

import (
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

// Schedule holds events for a version
type Schedule struct {
	// Version is the version that this schedule applies to
	Version Version `json:"version"`
	// Events are the events for this schedule
	Events []Event `json:"events,omitempty"`
}

// Version specifies an OCP release
type Version struct {
	// Major is the major version
	Major int `json:"major"`
	// Minor is the minor version
	Minor int `json:"minor"`
}

// Event is a moment in the schedule we care about
type Event struct {
	// Name is the event we are specifying
	Name LifecycleEvent `json:"name"`
	// Date is the published date for this event
	Date *metav1.Time `json:"date"`
	// DisplayDate is the date at which automation should actually switch over
	DisplayDate *metav1.Time `json:"display_date,omitempty"`
}

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
	LifecycleEventGenerallyAvailable LifecycleEvent = "general-availability"
	// LifecycleEventEndOfFullSupport marks the moment that a version is no longer supported fully.
	LifecycleEventEndOfFullSupport LifecycleEvent = "end-of-full-support"
	// LifecycleEventEndOfMaintenanceSupport marks the moment that a version is no longer supported.
	LifecycleEventEndOfMaintenanceSupport LifecycleEvent = "end-of-maintenance-support"
)

func lifecycleEventFor(event LifecycleEvent) (ocplifecycle.LifecycleEvent, bool) {
	mapping := map[LifecycleEvent]ocplifecycle.LifecycleEvent{
		LifecycleEventOpen:                    ocplifecycle.LifecycleEventOpen,
		LifecycleEventFeatureFreeze:           ocplifecycle.LifecycleEventFeatureFreeze,
		LifecycleEventCodeFreeze:              ocplifecycle.LifecycleEventCodeFreeze,
		LifecycleEventGenerallyAvailable:      ocplifecycle.LifecycleEventGenerallyAvailable,
		LifecycleEventEndOfMaintenanceSupport: ocplifecycle.LifecycleEventEndOfLife,
	}
	mapped, exists := mapping[event]
	return mapped, exists
}

const productOCP = "ocp"

func addToConfig(schedule Schedule, config *ocplifecycle.Config) *ocplifecycle.Config {
	if config == nil {
		config = &ocplifecycle.Config{}
	}
	if productConfig := (*config)[productOCP]; productConfig == nil {
		(*config)[productOCP] = map[string][]ocplifecycle.LifecyclePhase{}
	}
	var phases []ocplifecycle.LifecyclePhase
	for _, event := range schedule.Events {
		phase, ok := lifecycleEventFor(event.Name)
		if !ok {
			continue
		}
		var date *metav1.Time
		if event.DisplayDate != nil {
			date = event.DisplayDate
		} else {
			date = event.Date
		}
		if time.Now().Before(date.Time) {
			// we cannot expose future dates
			date = nil
		}
		phases = append(phases, ocplifecycle.LifecyclePhase{
			Event: phase,
			When:  date,
		})
	}
	sort.Slice(phases, func(i, j int) bool {
		return phases[j].When.Before(phases[i].When)
	})
	(*config)[productOCP][fmt.Sprintf("%d.%d", schedule.Version.Major, schedule.Version.Minor)] = phases
	return config
}
