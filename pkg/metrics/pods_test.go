package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPodLifecyclePlugin_Record(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	testCases := []struct {
		name     string
		pod      *corev1.Pod
		event    *PodLifecycleMetricsEvent
		expected []MetricsEvent
	}{
		{
			name: "pod ready with all timestamps",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-ns"},
				Status: corev1.PodStatus{
					StartTime: &metav1.Time{Time: now},
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(1 * time.Second)}},
						{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(2 * time.Second)}},
						{Type: corev1.ContainersReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(3 * time.Second)}},
						{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(4 * time.Second)}},
						{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(2 * time.Second)}},
					},
				},
			},
			event: &PodLifecycleMetricsEvent{PodName: "test-pod", Namespace: "test-ns"},
			expected: []MetricsEvent{
				&PodLifecycleMetricsEvent{
					PodName:             "test-pod",
					Namespace:           "test-ns",
					StartTime:           ptrTime(now),
					PodScheduledTime:    ptrTime(now.Add(1 * time.Second)),
					InitializedTime:     ptrTime(now.Add(2 * time.Second)),
					ReadyToStartTime:    ptrTime(now.Add(2 * time.Second)),
					ContainersReadyTime: ptrTime(now.Add(3 * time.Second)),
					ReadyTime:           ptrTime(now.Add(4 * time.Second)),
					ConditionTransitionTimes: map[string]time.Time{
						"PodScheduled":              now.Add(1 * time.Second),
						"Initialized":               now.Add(2 * time.Second),
						"PodReadyToStartContainers": now.Add(2 * time.Second),
						"ContainersReady":           now.Add(3 * time.Second),
						"Ready":                     now.Add(4 * time.Second),
					},
					SchedulingLatency:     ptrDuration(1 * time.Second),
					InitializationLatency: ptrDuration(1 * time.Second),
					ReadyLatency:          ptrDuration(4 * time.Second),
				},
			},
		},
		{
			name:     "pod not found",
			pod:      &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "another", Namespace: "test-ns"}},
			event:    &PodLifecycleMetricsEvent{PodName: "notfound", Namespace: "test-ns"},
			expected: []MetricsEvent{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := NewPodLifecyclePlugin(
				context.Background(),
				logrus.WithField("test", tc.name),
				fake.NewClientBuilder().WithObjects(tc.pod).Build(),
			)

			plugin.Record(tc.event)
			if diff := cmp.Diff(tc.expected, plugin.Events()); diff != "" {
				t.Errorf("unexpected events (-want +got):\n%s", diff)
			}

		})
	}
}

func ptrTime(t time.Time) *time.Time             { return &t }
func ptrDuration(d time.Duration) *time.Duration { return &d }
