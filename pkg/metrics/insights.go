package metrics

import (
	"time"
)

// InsightsEvent defines a test platform insight event.
type InsightsEvent struct {
	Name              string         `json:"name"`
	AdditionalContext map[string]any `json:"additional_context,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
}

// Store appends this insight event to the controller’s insights bucket.
func (ie InsightsEvent) Store(mc *MetricsAgent) {
	mc.insights = append(mc.insights, ie)
}

// Category returns the event category.
func (ie InsightsEvent) Category() string {
	return "test_platform_insights"
}

// SetTimestamp sets the timestamp of the event.
func (ie *InsightsEvent) SetTimestamp(t time.Time) {
	ie.Timestamp = t
}
