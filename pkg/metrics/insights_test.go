package metrics

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func Test_insightsPlugin_Record(t *testing.T) {
	tests := []struct {
		name           string
		events         []MetricsEvent
		expectedEvents []MetricsEvent
	}{
		{
			name:   "no events",
			events: []MetricsEvent{},
		},
		{
			name: "record single insights event",
			events: []MetricsEvent{
				&InsightsEvent{
					Name: "test-event",
					AdditionalContext: map[string]any{
						"key1": "value1",
						"key2": 1,
					},
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			expectedEvents: []MetricsEvent{
				&InsightsEvent{
					Name: "test-event",
					AdditionalContext: map[string]any{
						"key1": "value1",
						"key2": 1,
					},
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "record multiple insights events",
			events: []MetricsEvent{
				&InsightsEvent{
					Name:      "event-1",
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
				&InsightsEvent{
					Name:      "event-2",
					Timestamp: time.Date(2025, time.January, 1, 1, 0, 0, 0, time.UTC),
				},
			},
			expectedEvents: []MetricsEvent{
				&InsightsEvent{
					Name:      "event-1",
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
				&InsightsEvent{
					Name:      "event-2",
					Timestamp: time.Date(2025, time.January, 1, 1, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "ignore non-insights event",
			events: []MetricsEvent{
				&InsightsEvent{
					Name:      "insights-event",
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
				&BuildEvent{
					Name:      "build-event",
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			expectedEvents: []MetricsEvent{
				&InsightsEvent{
					Name:      "insights-event",
					Timestamp: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newInsightsPlugin()

			for _, event := range tt.events {
				p.Record(event)
			}

			if diff := cmp.Diff(tt.expectedEvents, p.Events()); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
