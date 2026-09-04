package util

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestWaitForCompletedPodDeletion(t *testing.T) {
	const namespace = "test-ns"
	const name = "test-pod"

	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID("original-uid"),
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID("original-uid"),
		},
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}
	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID("original-uid"),
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	for _, tc := range []struct {
		name             string
		objects          []ctrlruntimeclient.Object
		interceptorsFunc func() interceptor.Funcs
		expectErr        bool
	}{
		{
			name:    "pod does not exist",
			objects: nil,
		},
		{
			name:    "running pod is left alone",
			objects: []ctrlruntimeclient.Object{runningPod},
		},
		{
			name:    "succeeded pod is deleted",
			objects: []ctrlruntimeclient.Object{succeededPod},
		},
		{
			name:    "failed pod is deleted",
			objects: []ctrlruntimeclient.Object{failedPod},
		},
		{
			name:    "delete returns NotFound",
			objects: []ctrlruntimeclient.Object{succeededPod},
			interceptorsFunc: func() interceptor.Funcs {
				return interceptor.Funcs{
					Delete: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.Object, _ ...ctrlruntimeclient.DeleteOption) error {
						return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound, Code: 404}}
					},
				}
			},
		},
		{
			name:      "delete returns Conflict while the same UID remains",
			objects:   []ctrlruntimeclient.Object{succeededPod},
			expectErr: true,
			interceptorsFunc: func() interceptor.Funcs {
				return interceptor.Funcs{
					Delete: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.Object, _ ...ctrlruntimeclient.DeleteOption) error {
						return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonConflict, Code: 409}}
					},
				}
			},
		},
		{
			name:      "delete returns unexpected error",
			objects:   []ctrlruntimeclient.Object{succeededPod},
			expectErr: true,
			interceptorsFunc: func() interceptor.Funcs {
				return interceptor.Funcs{
					Delete: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.Object, _ ...ctrlruntimeclient.DeleteOption) error {
						return &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInternalError, Code: 500}}
					},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := fakectrlruntimeclient.NewClientBuilder()
			if tc.objects != nil {
				builder = builder.WithObjects(tc.objects...)
			}
			if tc.interceptorsFunc != nil {
				builder = builder.WithInterceptorFuncs(tc.interceptorsFunc())
			}
			client := builder.Build()

			err := waitForCompletedPodDeletion(context.Background(), client, namespace, name)
			if tc.expectErr && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDeletePodWithUIDReconcilesAmbiguousDeletion(t *testing.T) {
	const (
		namespace = "test-ns"
		name      = "test-pod"
	)
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID("original-uid")}}

	t.Run("committed delete with lost response", func(t *testing.T) {
		var deleteCalls int
		client := fakectrlruntimeclient.NewClientBuilder().WithObjects(observed.DeepCopy()).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, client ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
				deleteCalls++
				if err := client.Delete(ctx, obj, opts...); err != nil {
					return err
				}
				return syscall.ECONNRESET
			},
		}).Build()

		if err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3}); err != nil {
			t.Fatalf("expected committed deletion to be reconciled: %v", err)
		}
		if deleteCalls != 1 {
			t.Fatalf("delete calls = %d, want 1", deleteCalls)
		}
		if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKeyFromObject(observed), &corev1.Pod{}); !apierrors.IsNotFound(err) {
			t.Fatalf("expected original pod to be gone, got %v", err)
		}
	})

	t.Run("transient confirmation GET is retried without another DELETE", func(t *testing.T) {
		var deleteCalls, getCalls int
		client := fakectrlruntimeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.Object, ...ctrlruntimeclient.DeleteOption) error {
				deleteCalls++
				return nil
			},
			Get: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.ObjectKey, _ ctrlruntimeclient.Object, _ ...ctrlruntimeclient.GetOption) error {
				getCalls++
				if getCalls == 1 {
					return fmt.Errorf("confirm deletion: %w", syscall.ECONNREFUSED)
				}
				return apierrors.NewNotFound(corev1.Resource("pods"), name)
			},
		}).Build()

		if err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3}); err != nil {
			t.Fatalf("expected transient confirmation failure to recover: %v", err)
		}
		if deleteCalls != 1 || getCalls != 2 {
			t.Fatalf("calls delete=%d get=%d, want delete=1 get=2", deleteCalls, getCalls)
		}
	})

	t.Run("permanent confirmation GET fails", func(t *testing.T) {
		forbidden := apierrors.NewForbidden(corev1.Resource("pods"), name, errors.New("denied"))
		client := fakectrlruntimeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.Object, ...ctrlruntimeclient.DeleteOption) error {
				return nil
			},
			Get: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.ObjectKey, ctrlruntimeclient.Object, ...ctrlruntimeclient.GetOption) error {
				return forbidden
			},
		}).Build()

		err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3})
		if !apierrors.IsForbidden(err) {
			t.Fatalf("expected permanent GET error, got %v", err)
		}
	})

	t.Run("same UID after transient DELETE is retried boundedly", func(t *testing.T) {
		var deleteCalls, getCalls int
		client := fakectrlruntimeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.Object, ...ctrlruntimeclient.DeleteOption) error {
				deleteCalls++
				return syscall.ECONNRESET
			},
			Get: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, _ ...ctrlruntimeclient.GetOption) error {
				getCalls++
				observed.DeepCopyInto(obj.(*corev1.Pod))
				return nil
			},
		}).Build()

		err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3})
		if !wait.Interrupted(err) || !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("expected bounded transient deletion failure, got %v", err)
		}
		if deleteCalls != 3 || getCalls != 3 {
			t.Fatalf("calls delete=%d get=%d, want 3 each", deleteCalls, getCalls)
		}
	})

	t.Run("accepted DELETE polls a remaining UID without redundant DELETEs", func(t *testing.T) {
		var deleteCalls, getCalls int
		client := fakectrlruntimeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.Object, ...ctrlruntimeclient.DeleteOption) error {
				deleteCalls++
				return nil
			},
			Get: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, _ ...ctrlruntimeclient.GetOption) error {
				getCalls++
				observed.DeepCopyInto(obj.(*corev1.Pod))
				return nil
			},
		}).Build()

		err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3})
		if !wait.Interrupted(err) {
			t.Fatalf("expected bounded confirmation failure, got %v", err)
		}
		if deleteCalls != 1 || getCalls != 3 {
			t.Fatalf("calls delete=%d get=%d, want delete=1 get=3", deleteCalls, getCalls)
		}
	})
}

func TestDeletePodWithUIDSameUIDConflictFails(t *testing.T) {
	const (
		namespace = "test-ns"
		name      = "test-pod"
	)
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID("original-uid")}}
	var deleteCalls int
	client := fakectrlruntimeclient.NewClientBuilder().WithObjects(observed.DeepCopy()).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(context.Context, ctrlruntimeclient.WithWatch, ctrlruntimeclient.Object, ...ctrlruntimeclient.DeleteOption) error {
			deleteCalls++
			return apierrors.NewConflict(corev1.Resource("pods"), name, errors.New("conflict"))
		},
	}).Build()

	err := deletePodWithUID(context.Background(), client, observed, wait.Backoff{Steps: 3})
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected same-UID conflict to fail permanently, got %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestDeletePodWithUIDDoesNotDeleteReplacement(t *testing.T) {
	const (
		namespace = "test-ns"
		name      = "test-pod"
	)
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID("old-uid")}}
	replacement := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID("replacement-uid")}}
	var preconditionUID types.UID
	client := fakectrlruntimeclient.NewClientBuilder().WithObjects(replacement).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(_ context.Context, _ ctrlruntimeclient.WithWatch, _ ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
			deleteOptions := &ctrlruntimeclient.DeleteOptions{}
			deleteOptions.ApplyOptions(opts)
			if deleteOptions.Preconditions != nil && deleteOptions.Preconditions.UID != nil {
				preconditionUID = *deleteOptions.Preconditions.UID
			}
			return apierrors.NewConflict(corev1.Resource("pods"), name, errors.New("UID precondition mismatch"))
		},
	}).Build()

	if err := DeletePodWithUID(context.Background(), client, observed); err != nil {
		t.Fatalf("stale deletion should be harmless: %v", err)
	}
	if preconditionUID != observed.UID {
		t.Fatalf("delete precondition UID = %q, want %q", preconditionUID, observed.UID)
	}
	current := &corev1.Pod{}
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, current); err != nil {
		t.Fatalf("replacement pod was deleted: %v", err)
	}
	if current.UID != replacement.UID {
		t.Fatalf("got pod UID %q, want replacement UID %q", current.UID, replacement.UID)
	}
}

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
			Spec: corev1.PodSpec{NodeName: "scheduled"},
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
			Spec: corev1.PodSpec{NodeName: "scheduled"},
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
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
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
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
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
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
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
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
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
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
			Status: corev1.PodStatus{
				Phase:             corev1.PodPending,
				ContainerStatuses: []corev1.ContainerStatus{running, waiting0},
			},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "pod is pending outside limit",
		pod: corev1.Pod{
			Spec: corev1.PodSpec{NodeName: "scheduled"},
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
			Spec: corev1.PodSpec{NodeName: "scheduled"},
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
			Spec: corev1.PodSpec{NodeName: "scheduled"},
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
		name: "scheduled pod with no container statuses within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "scheduled pod with no container statuses outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Spec:       corev1.PodSpec{NodeName: "scheduled"},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		err: errors.New("container statuses have not been set by kubelet in 1h0m0s"),
	}, {
		name: "unscheduled pod pending within limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: withinLimit},
			Spec:       corev1.PodSpec{NodeName: ""}, // Explicitly unscheduled
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		next: withinLimit.Add(timeout),
	}, {
		name: "unscheduled pod pending outside limit",
		pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: outsideLimit},
			Spec:       corev1.PodSpec{NodeName: ""}, // Explicitly unscheduled
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		err: errors.New("pod has not been scheduled in 1h0m0s"),
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := checkPending(tc.pod, timeout, now)
			testhelper.Diff(t, "next", ret, tc.next)
			testhelper.Diff(t, "error", err, tc.err, testhelper.EquateErrorMessage)
		})
	}
}
