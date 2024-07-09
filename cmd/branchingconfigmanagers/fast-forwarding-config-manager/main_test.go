package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

func TestReconcile(t *testing.T) {
	testCases := []struct {
		name            string
		now             time.Time
		lifecycleConfig ocplifecycle.Config
		job             *prowconfig.Periodic
		expectedJob     *prowconfig.Periodic
	}{
		{
			name: "first next event is development branch opening for 4.11, expect to append --future-release=4.11 in arguments",
			now:  time.Date(1976, time.January, 28, 0, 0, 0, 0, time.UTC),
			lifecycleConfig: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.10": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.March, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.January, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1975, time.December, 20, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1975, time.September, 10, 0, 0, 0, 0, time.UTC)}},
					},
					"4.11": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.August, 5, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.June, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1976, time.May, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1976, time.January, 29, 0, 0, 0, 0, time.UTC)}},
					},
				},
			},

			job: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.10", "--future-release=4.10"}}},
					},
				},
			},

			expectedJob: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.10", "--future-release=4.10", "--future-release=4.11"}}},
					},
				},
			},
		},
		{
			name: "last event was development branch opening for 4.11",
			now:  time.Date(1976, time.January, 31, 0, 0, 0, 0, time.UTC),
			lifecycleConfig: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.10": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.March, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.January, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1975, time.December, 20, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1975, time.September, 10, 0, 0, 0, 0, time.UTC)}},
					},
					"4.11": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.August, 5, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.June, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1976, time.May, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1976, time.January, 29, 0, 0, 0, 0, time.UTC)}},
					},
				},
			},

			job: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.10", "--future-release=4.10", "--future-release=4.11"}}},
					},
				},
			},

			expectedJob: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.11", "--future-release=4.11"}}},
					},
				},
			},
		},
		{
			name: "last event was feature freeze for 4.11",
			now:  time.Date(1976, time.May, 16, 0, 0, 0, 0, time.UTC),
			lifecycleConfig: ocplifecycle.Config{
				"ocp": map[string][]ocplifecycle.LifecyclePhase{
					"4.10": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.March, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.January, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1975, time.December, 20, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1975, time.September, 10, 0, 0, 0, 0, time.UTC)}},
					},
					"4.11": {
						{Event: "generally-available", When: &metav1.Time{Time: time.Date(1976, time.August, 5, 0, 0, 0, 0, time.UTC)}},
						{Event: "code-freeze", When: &metav1.Time{Time: time.Date(1976, time.June, 30, 0, 0, 0, 0, time.UTC)}},
						{Event: "feature-freeze", When: &metav1.Time{Time: time.Date(1976, time.May, 15, 0, 0, 0, 0, time.UTC)}},
						{Event: "open", When: &metav1.Time{Time: time.Date(1976, time.January, 29, 0, 0, 0, 0, time.UTC)}},
					},
				},
			},

			job: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.10", "--future-release=4.10", "--future-release=4.11"}}},
					},
				},
			},

			expectedJob: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Spec: &corev1.PodSpec{
						Containers: []corev1.Container{{Args: []string{"--current-release=4.11", "--future-release=4.11"}}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := reconcile(tc.lifecycleConfig, tc.now, tc.job)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.job, tc.expectedJob, cmpopts.IgnoreUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatal(diff)
			}
		})
	}

}
