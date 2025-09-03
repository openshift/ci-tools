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
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-pod",
					Namespace:         "test-ns",
					CreationTimestamp: metav1.Time{Time: now.Add(-1 * time.Second)},
				},
				Status: corev1.PodStatus{
					Phase:     corev1.PodRunning,
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
					PodName:      "test-pod",
					Namespace:    "test-ns",
					CreationTime: ptrTime(now.Add(-1 * time.Second)),
					StartTime:    ptrTime(now),
					PodPhase:     corev1.PodRunning,
					ConditionTransitionTimes: map[string]time.Time{
						string(corev1.PodScheduled):              now.Add(1 * time.Second),
						string(corev1.PodInitialized):            now.Add(2 * time.Second),
						string(corev1.PodReadyToStartContainers): now.Add(2 * time.Second),
						string(corev1.ContainersReady):           now.Add(3 * time.Second),
						string(corev1.PodReady):                  now.Add(4 * time.Second),
					},
					SchedulingLatency:     ptrDuration(2 * time.Second),
					InitializationLatency: ptrDuration(1 * time.Second),
					ReadyLatency:          ptrDuration(5 * time.Second),
				},
			},
		},
		{
			name: "completed pod with all metrics",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "completed-pod",
					Namespace:         "test-ns",
					CreationTimestamp: metav1.Time{Time: now.Add(-10 * time.Second)},
				},
				Status: corev1.PodStatus{
					Phase:     corev1.PodSucceeded,
					StartTime: &metav1.Time{Time: now.Add(-9 * time.Second)},
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(-8 * time.Second)}},
						{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(-7 * time.Second)}},
						{Type: corev1.ContainersReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(-6 * time.Second)}},
						{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: now.Add(-5 * time.Second)}},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   0,
									FinishedAt: metav1.Time{Time: now.Add(-2 * time.Second)},
								},
							},
						},
					},
				},
			},
			event: &PodLifecycleMetricsEvent{PodName: "completed-pod", Namespace: "test-ns"},
			expected: []MetricsEvent{
				&PodLifecycleMetricsEvent{
					PodName:        "completed-pod",
					Namespace:      "test-ns",
					CreationTime:   ptrTime(now.Add(-10 * time.Second)),
					StartTime:      ptrTime(now.Add(-9 * time.Second)),
					CompletionTime: ptrTime(now.Add(-2 * time.Second)),
					PodPhase:       corev1.PodSucceeded,
					ConditionTransitionTimes: map[string]time.Time{
						string(corev1.PodScheduled):    now.Add(-8 * time.Second),
						string(corev1.PodInitialized):  now.Add(-7 * time.Second),
						string(corev1.ContainersReady): now.Add(-6 * time.Second),
						string(corev1.PodReady):        now.Add(-5 * time.Second),
					},
					SchedulingLatency:     ptrDuration(2 * time.Second),
					InitializationLatency: ptrDuration(1 * time.Second),
					ReadyLatency:          ptrDuration(5 * time.Second),
					CompletionLatency:     ptrDuration(8 * time.Second),
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

func TestGetPodCompletionTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	testCases := []struct {
		name     string
		pod      *corev1.Pod
		expected *time.Time
	}{
		{
			name: "no terminated containers",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					},
				},
			},
			expected: nil,
		},
		{
			name: "single terminated container",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: now},
								},
							},
						},
					},
				},
			},
			expected: &now,
		},
		{
			name: "multiple terminated containers - returns latest",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: now.Add(-5 * time.Second)},
								},
							},
						},
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: now.Add(-2 * time.Second)},
								},
							},
						},
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: now.Add(-10 * time.Second)},
								},
							},
						},
					},
				},
			},
			expected: ptrTime(now.Add(-2 * time.Second)),
		},
		{
			name: "only failed init containers with non-zero exit code",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   1,
									FinishedAt: metav1.Time{Time: now.Add(-3 * time.Second)},
								},
							},
						},
					},
				},
			},
			expected: ptrTime(now.Add(-3 * time.Second)),
		},
		{
			name: "init containers with zero exit code - should be ignored",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   0,
									FinishedAt: metav1.Time{Time: now.Add(-3 * time.Second)},
								},
							},
						},
					},
				},
			},
			expected: nil,
		},
		{
			name: "regular containers take precedence over init containers",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: now.Add(-1 * time.Second)},
								},
							},
						},
					},
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   1,
									FinishedAt: metav1.Time{Time: now.Add(-2 * time.Second)},
								},
							},
						},
					},
				},
			},
			expected: ptrTime(now.Add(-1 * time.Second)),
		},
		{
			name: "mixed init containers - only failed ones count",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   0,
									FinishedAt: metav1.Time{Time: now.Add(-1 * time.Second)},
								},
							},
						},
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   1,
									FinishedAt: metav1.Time{Time: now.Add(-3 * time.Second)},
								},
							},
						},
						{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									ExitCode:   2,
									FinishedAt: metav1.Time{Time: now.Add(-2 * time.Second)},
								},
							},
						},
					},
				},
			},
			expected: ptrTime(now.Add(-2 * time.Second)),
		},
		{
			name: "empty pod status",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{},
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getPodCompletionTime(tc.pod)

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}
