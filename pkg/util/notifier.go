package util

import (
	corev1 "k8s.io/api/core/v1"
)

// ContainerNotifier receives updates about the status of a poll action on a pod. The caller
// is required to define what notifications are made.
type ContainerNotifier interface {
	// Notify indicates that the provided container name has transitioned to an appropriate state and
	// any per container actions should be taken.
	Notify(pod *corev1.Pod, containerName string)
	// Complete indicates the specified pod has completed execution, been deleted, or that no further
	// Notify() calls can be made.
	Complete(podName string)
	// Done returns a channel that can be used to wait for the specified pod name to complete the work it has pending.
	Done(podName string) <-chan struct{}
}

// NopNotifier takes no action when notified.
var NopNotifier = nopNotifier{}

type nopNotifier struct{}

func (nopNotifier) Notify(_ *corev1.Pod, _ string) {}
func (nopNotifier) Complete(_ string)              {}
func (nopNotifier) Done(string) <-chan struct{} {
	ret := make(chan struct{})
	close(ret)
	return ret
}
