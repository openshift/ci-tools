package util

import (
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestCheckPending(t *testing.T) {
	timeout, now := 30*time.Minute, time.Time{}
	withinLimit := metav1.Time{Time: now.Add(-time.Minute)}
	outsideLimit := metav1.Time{Time: now.Add(-time.Hour)}
	running := corev1.ContainerStatus{
		Name: "running",
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{},
		},
	}
	waiting0 := corev1.ContainerStatus{
		Name: "waiting0",
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
		},
	}
	waiting1 := corev1.ContainerStatus{
		Name: "waiting1",
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
		},
	}
	terminatedWithin := corev1.ContainerStatus{
		Name: "terminated",
		State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				FinishedAt: withinLimit,
			},
		},
	}
	terminatedOutside := corev1.ContainerStatus{
		Name: "terminated",
		State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				FinishedAt: outsideLimit,
			},
		},
	}
	for _, tc := range []struct {
		// input
		name string
		pod  corev1.Pod
		// output
		next time.Time
		err  error
	}{{
		name: "pod status is unknown",
		pod: corev1.Pod{Status: corev1.PodStatus{
			Phase: corev1.PodUnknown,
		}},
		next: now.Add(timeout),
	}, {
		name: "pod is running",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: outsideLimit,
			},
			Status: corev1.PodStatus{
				Phase:             corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{running},
			},
		},
		next: now.Add(timeout),
	}, {
		name: "pod succeeded",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodSucceeded,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "succeeded",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 0,
						},
					},
				}},
			},
		},
	}, {
		name: "pod failed",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodFailed,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "failed",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				}},
			},
		},
	}, {
		name: "first init container is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase:                 corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{running},
				ContainerStatuses:     []corev1.ContainerStatus{waiting0},
			},
		},
		next: now.Add(timeout),
	}, {
		name: "init container is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside, running,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting0},
			},
		},
		next: now.Add(timeout),
	}, {
		name: "first init container is waiting within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Status: corev1.PodStatus{
				Phase:                 corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{waiting0},
				ContainerStatuses:     []corev1.ContainerStatus{waiting1},
			},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "first init container is waiting outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase:                 corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{waiting0},
				ContainerStatuses:     []corev1.ContainerStatus{waiting1},
			},
		},
		err: errors.New("containers have not started in 1h0m0s: waiting0, waiting1"),
	}, {
		name: "init container is waiting within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedWithin, waiting0,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting1},
			},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "init container is waiting outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside, waiting0,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting1},
			},
		},
		err: errors.New("containers have not started in 1h0m0s: waiting0, waiting1"),
	}, {
		name: "pod is pending within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Status: corev1.PodStatus{
				Phase:             corev1.PodPending,
				ContainerStatuses: []corev1.ContainerStatus{running, waiting0},
			},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "pod is pending outside limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting0},
			},
		},
		err: errors.New("containers have not started in 1h0m0s: waiting0"),
	}, {
		name: "pod with init container is pending within limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedWithin,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting0},
			},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "pod with init container is pending outside limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting0},
			},
		},
		err: errors.New("containers have not started in 1h0m0s: waiting0"),
	}, {
		name: "pod is pending inside limit without container information",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		next: withinLimit.Add(timeout),
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := checkPending(tc.pod, timeout, now)
			testhelper.Diff(t, "next", ret, tc.next)
			testhelper.Diff(t, "error", err, tc.err, testhelper.EquateErrorMessage)
		})
	}
}
