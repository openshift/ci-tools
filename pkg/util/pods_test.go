package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestPodHasStarted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pod      corev1.Pod
		expected bool
	}{{
		name: "pod is pending",
		pod: corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
	}, {
		name: "pod is pending, init containers are not running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "waiting",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{},
					},
				}, {
					Name: "terminated",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{},
					},
				}},
			},
		},
	}, {
		name: "pod is pending, infrastructure init container is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "cp-secret-wrapper",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				}},
			},
		},
	}, {
		name: "pod is pending, init container is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{{
					Name: "running",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				}},
			},
		},
		expected: true,
	}, {
		name: "pod is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		expected: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret := podHasStarted(&tc.pod)
			if ret != tc.expected {
				t.Fatalf("got %v, want %v", ret, tc.expected)
			}
		})
	}
}
