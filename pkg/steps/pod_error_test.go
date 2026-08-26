package steps

import (
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodStepErrorContract(t *testing.T) {
	cause := errors.New("pod failed")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "failed-pod"}}
	err := fmt.Errorf("outer context: %w", &PodStepError{Pod: pod, err: cause})

	var podStepErr *PodStepError
	if !errors.As(err, &podStepErr) {
		t.Fatalf("expected errors.As to find PodStepError in %v", err)
	}
	if podStepErr.Pod != pod {
		t.Fatalf("expected observed pod to be retained, got %#v", podStepErr.Pod)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected errors.Is to find wrapped cause in %v", err)
	}
}
