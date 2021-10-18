package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

func TestAddToConfig(t *testing.T) {
	var testCases = []struct {
		name             string
		schedule         Schedule
		config, expected *ocplifecycle.Config
	}{
		{
			name: "nil config in",
			schedule: Schedule{
				Version: ocplifecycle.MajorMinor{Major: 4, Minor: 1},
				Events:  []Event{{Name: ocplifecycle.LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}}},
			},
			config: nil,
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
		},
		{
			name: "updates existing date",
			schedule: Schedule{
				Version: ocplifecycle.MajorMinor{Major: 4, Minor: 1},
				Events:  []Event{{Name: ocplifecycle.LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)}}},
			},
			config: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
					}},
				},
			},
		},
		{
			name: "adds new date",
			schedule: Schedule{
				Version: ocplifecycle.MajorMinor{Major: 4, Minor: 1},
				Events: []Event{
					{Name: ocplifecycle.LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}},
					{Name: ocplifecycle.LifecycleEventGenerallyAvailable, Date: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)}},
				},
			},
			config: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {
						{
							Event: ocplifecycle.LifecycleEventOpen,
							When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
						},
					},
				},
			},
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {
						{
							Event: ocplifecycle.LifecycleEventOpen,
							When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
						},
						{
							Event: ocplifecycle.LifecycleEventGenerallyAvailable,
							When:  &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
						},
					},
				},
			},
		},
		{
			name: "adds new version",
			schedule: Schedule{
				Version: ocplifecycle.MajorMinor{Major: 4, Minor: 1},
				Events: []Event{
					{Name: ocplifecycle.LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}},
				},
			},
			config: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.0": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.0": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
		},
		{
			name: "prefers display date",
			schedule: Schedule{
				Version: ocplifecycle.MajorMinor{Major: 4, Minor: 1},
				Events: []Event{{
					Name:        ocplifecycle.LifecycleEventOpen,
					Date:        &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					DisplayDate: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
				}},
			},
			config: nil,
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
					}},
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.config = addToConfig(testCase.schedule, testCase.config)
			if diff := cmp.Diff(testCase.expected, testCase.config); diff != "" {
				t.Errorf("%s: got incorrect config after update: %v", testCase.name, diff)
			}
		})
	}
}
