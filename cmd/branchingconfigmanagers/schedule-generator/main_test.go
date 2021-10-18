package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestReadSchedules(t *testing.T) {
	config, err := readSchedules("testdata/schedules")
	if err != nil {
		t.Fatalf("could not read schedules: %v", err)
	}
	testhelper.CompareWithFixture(t, config)
}

func TestValidateSchedules(t *testing.T) {
	testCases := []struct {
		name           string
		config         ocplifecycle.Config
		expectedErrors string
	}{
		{
			name: "happy case",
			config: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.9": {
						{
							Event: ocplifecycle.LifecycleEventGenerallyAvailable,
							When:  &metav1.Time{Time: time.Date(2000, 5, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventCodeFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 4, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventFeatureFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 3, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventOpen,
							When:  &metav1.Time{Time: time.Date(2000, 2, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
					},
				},
			},
		},
		{
			name: "sad case, dates are not sorted",
			config: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.9": {
						{
							Event: ocplifecycle.LifecycleEventGenerallyAvailable,
							When:  &metav1.Time{Time: time.Date(2000, 1, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventCodeFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 4, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventFeatureFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 3, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventOpen,
							When:  &metav1.Time{Time: time.Date(2000, 2, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
					},
				},
			},
			expectedErrors: "version 4.9: event `code-freeze` date is after event `generally-available`",
		},
		{
			name: "sad case, unknown event",
			config: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.9": {
						{
							Event: "beer festival",
							When:  &metav1.Time{Time: time.Date(2000, 5, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventCodeFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 4, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventFeatureFreeze,
							When:  &metav1.Time{Time: time.Date(2000, 3, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
						{
							Event: ocplifecycle.LifecycleEventOpen,
							When:  &metav1.Time{Time: time.Date(2000, 2, 1, 0, 0, 0, 0, metav1.Now().Location())},
						},
					},
				},
			},
			expectedErrors: "unknown event: beer festival",
		},
	}

	for _, tc := range testCases {
		err := validateSchedules(tc.config)
		if err == nil && tc.expectedErrors != "" {
			t.Fatalf("expected error: %s but got nil", tc.expectedErrors)
		}

		if err != nil {
			if diff := cmp.Diff(tc.expectedErrors, err.Error()); diff != "" {
				t.Fatal(diff)
			}
		}
	}
}
