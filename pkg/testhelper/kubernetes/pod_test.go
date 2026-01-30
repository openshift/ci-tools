package testhelper_test

import (
	"context"
	"testing"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelper "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

func TestFakePodExecutor_AutoSchedule(t *testing.T) {
	tests := []struct {
		name         string
		autoSchedule bool
		setNodeName  string
		wantNodeName string
	}{
		{
			name:         "AutoSchedule enabled - pod gets scheduled",
			autoSchedule: true,
			wantNodeName: "fake-node",
		},
		{
			name:         "AutoSchedule enabled but pod created with spec.NodeName - field left unchanged",
			autoSchedule: true,
			setNodeName:  "unchanged",
			wantNodeName: "unchanged",
		},
		{
			name:         "AutoSchedule disabled - pod remains unscheduled",
			autoSchedule: false,
			wantNodeName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &testhelper.FakePodExecutor{
				LoggingClient: loggingclient.New(fakectrlruntimeclient.NewClientBuilder().Build(), nil),
				AutoSchedule:  tt.autoSchedule,
			}

			pod := &coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-ns",
				},
				Spec: coreapi.PodSpec{
					NodeName: tt.setNodeName,
				},
			}

			if err := executor.Create(context.Background(), pod); err != nil {
				t.Fatalf("Failed to create pod: %v", err)
			}

			if pod.Spec.NodeName != tt.wantNodeName {
				t.Errorf("Expected NodeName=%q, got %q", tt.wantNodeName, pod.Spec.NodeName)
			}

			if pod.CreationTimestamp.IsZero() {
				t.Error("Expected CreationTimestamp to be set, but it was zero")
			}
		})
	}
}
