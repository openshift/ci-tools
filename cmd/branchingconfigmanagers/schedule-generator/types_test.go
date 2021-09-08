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
				Version: Version{Major: 4, Minor: 1},
				Events:  []Event{{Name: LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}}},
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
				Version: Version{Major: 4, Minor: 1},
				Events:  []Event{{Name: LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)}}},
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
				Version: Version{Major: 4, Minor: 1},
				Events: []Event{
					{Name: LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}},
					{Name: LifecycleEventGenerallyAvailable, Date: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)}},
				},
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
						Event: ocplifecycle.LifecycleEventGenerallyAvailable,
						When:  &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
					}, {
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
		},
		{
			name: "adds new version",
			schedule: Schedule{
				Version: Version{Major: 4, Minor: 1},
				Events: []Event{
					{Name: LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}},
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
			name: "all mappings, output sorted correctly",
			schedule: Schedule{
				Version: Version{Major: 4, Minor: 1},
				Events: []Event{
					{Name: LifecycleEventOpen, Date: &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)}},
					{Name: LifecycleEventFeatureFreeze, Date: &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)}},
					{Name: LifecycleEventCodeFreeze, Date: &metav1.Time{Time: time.Date(3, 3, 3, 3, 3, 3, 3, time.UTC)}},
					{Name: LifecycleEventGenerallyAvailable, Date: &metav1.Time{Time: time.Date(4, 4, 4, 4, 4, 4, 4, time.UTC)}},
					{Name: LifecycleEventEndOfFullSupport, Date: &metav1.Time{Time: time.Date(5, 5, 5, 5, 5, 5, 5, time.UTC)}},
					{Name: LifecycleEventEndOfMaintenanceSupport, Date: &metav1.Time{Time: time.Date(6, 6, 6, 6, 6, 6, 6, time.UTC)}},
				},
			},
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventEndOfLife,
						When:  &metav1.Time{Time: time.Date(6, 6, 6, 6, 6, 6, 6, time.UTC)},
					}, {
						Event: ocplifecycle.LifecycleEventGenerallyAvailable,
						When:  &metav1.Time{Time: time.Date(4, 4, 4, 4, 4, 4, 4, time.UTC)},
					}, {
						Event: ocplifecycle.LifecycleEventCodeFreeze,
						When:  &metav1.Time{Time: time.Date(3, 3, 3, 3, 3, 3, 3, time.UTC)},
					}, {
						Event: ocplifecycle.LifecycleEventFeatureFreeze,
						When:  &metav1.Time{Time: time.Date(2, 2, 2, 2, 2, 2, 2, time.UTC)},
					}, {
						Event: ocplifecycle.LifecycleEventOpen,
						When:  &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					}},
				},
			},
		},
		{
			name: "prefers display date",
			schedule: Schedule{
				Version: Version{Major: 4, Minor: 1},
				Events: []Event{{
					Name:        LifecycleEventOpen,
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
		{
			name: "hides future dates",
			schedule: Schedule{
				Version: Version{Major: 4, Minor: 1},
				Events: []Event{{
					Name:        LifecycleEventOpen,
					Date:        &metav1.Time{Time: time.Date(1, 1, 1, 1, 1, 1, 1, time.UTC)},
					DisplayDate: &metav1.Time{Time: time.Date(9999, 2, 2, 2, 2, 2, 2, time.UTC)},
				}, {
					Name: LifecycleEventGenerallyAvailable,
					Date: &metav1.Time{Time: time.Date(9999, 1, 1, 1, 1, 1, 1, time.UTC)},
				}},
			},
			config: nil,
			expected: &ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.1": {{
						Event: ocplifecycle.LifecycleEventOpen,
					}, {
						Event: ocplifecycle.LifecycleEventGenerallyAvailable,
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
