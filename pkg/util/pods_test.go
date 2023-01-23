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
	t0 := time.Time{}
	withinLimit := metav1.Time{Time: t0.Add(-time.Minute)}
	outsideLimit := metav1.Time{Time: t0.Add(-time.Hour)}
	running := corev1.ContainerStatus{
		Name: "running",
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{},
		},
	}
	waiting := corev1.ContainerStatus{
		Name: "waiting",
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
	err := errors.New(`container "waiting" has not started in 30m0s`)
	for _, tc := range []struct {
		name     string
		pod      corev1.Pod
		expected error
	}{{
		name: "pod status is unknown",
		pod:  corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodUnknown}},
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
					Name: "succeeded",
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
				ContainerStatuses:     []corev1.ContainerStatus{waiting},
			},
		},
	}, {
		name: "init container is running",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside, running,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting},
			},
		},
	}, {
		name: "first init container is waiting within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Status: corev1.PodStatus{
				Phase:                 corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{waiting},
				ContainerStatuses:     []corev1.ContainerStatus{waiting},
			},
		},
	}, {
		name: "first init container is waiting outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase:                 corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{waiting},
				ContainerStatuses:     []corev1.ContainerStatus{waiting},
			},
		},
		expected: err,
	}, {
		name: "init container is waiting within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedWithin, waiting,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting},
			},
		},
	}, {
		name: "init container is waiting outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside, waiting,
				},
				ContainerStatuses: []corev1.ContainerStatus{waiting},
			},
		},
		expected: err,
	}, {
		name: "pod is pending within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Status: corev1.PodStatus{
				Phase:             corev1.PodPending,
				ContainerStatuses: []corev1.ContainerStatus{running, waiting},
			},
		},
	}, {
		name: "pod is pending outside limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting},
			},
		},
		expected: err,
	}, {
		name: "pod with init container is pending within limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedWithin,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting},
			},
		},
	}, {
		name: "pod with init container is pending outside limit",
		pod: corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{
					terminatedOutside,
				},
				ContainerStatuses: []corev1.ContainerStatus{running, waiting},
			},
		},
		expected: err,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret := checkPending(tc.pod, 30*time.Minute, t0)
			testhelper.Diff(t, "error", ret, tc.expected, testhelper.EquateErrorMessage)
		})
	}
}
