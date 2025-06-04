package metrics

import (
	"time"

	buildapi "github.com/openshift/api/build/v1"
)

// BuildEvent defines a build event for the metrics system.
type BuildEvent struct {
	Namespace         string         `json:"namespace"`
	Name              string         `json:"name"`
	StartTime         time.Time      `json:"start_time"`
	CompletionTime    time.Time      `json:"completion_time"`
	DurationSeconds   int            `json:"duration_seconds"`
	Status            string         `json:"status"`
	Reason            string         `json:"reason,omitempty"`
	OutputImage       string         `json:"output_image,omitempty"`
	AdditionalContext map[string]any `json:"additional_context,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
	ForImage          string         `json:"for_image,omitempty"`
}

// Store appends this insight event to the controller’s builds bucket.
func (be BuildEvent) Store(mc *MetricsAgent) {
	mc.builds = append(mc.builds, be)
}

// Category returns the event category.
func (be BuildEvent) Category() string {
	return "openshift_builds"
}

// SetTimestamp sets the timestamp of the event.
func (be *BuildEvent) SetTimestamp(t time.Time) {
	be.Timestamp = t
}

// RecordBuildEvent extracts data from Build and records an event.
func (mc *MetricsAgent) RecordBuildEvent(b buildapi.Build, forImage string) {
	start := b.Status.StartTimestamp.Time
	completion := b.Status.CompletionTimestamp.Time

	duration := 0
	if !start.IsZero() && !completion.IsZero() {
		duration = int(completion.Sub(start).Seconds())
	}

	be := &BuildEvent{
		Namespace:       b.Namespace,
		Name:            b.Name,
		StartTime:       start,
		CompletionTime:  completion,
		DurationSeconds: duration,
		Status:          string(b.Status.Phase),
		Reason:          string(b.Status.Reason),
		OutputImage:     b.Spec.Output.To.Name,
		Timestamp:       time.Now(),
		ForImage:        forImage,
	}

	mc.Record(be)
}
