package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

// Schedule holds events for a version
type Schedule struct {
	// Version is the version that this schedule applies to
	Version ocplifecycle.MajorMinor `json:"version"`
	// Events are the events for this schedule
	Events []Event `json:"events,omitempty"`
}

// Event is a moment in the schedule we care about
type Event struct {
	// Name is the event we are specifying
	Name ocplifecycle.LifecycleEvent `json:"name"`
	// Date is the published date for this event
	Date *metav1.Time `json:"date"`
	// DisplayDate is the date at which automation should actually switch over
	DisplayDate *metav1.Time `json:"display_date,omitempty"`
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
		var date *metav1.Time
		if event.DisplayDate != nil {
			date = event.DisplayDate
		} else {
			date = event.Date
		}
		phases = append(phases, ocplifecycle.LifecyclePhase{
			Event: event.Name,
			When:  date,
		})
	}

	(*config)[productOCP][schedule.Version.String()] = phases
	return config
}
