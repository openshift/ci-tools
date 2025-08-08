package util

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWaitUntilNamespaceIsPrivileged(t *testing.T) {
	const testNS = "test-ns"
	tests := []struct {
		name        string
		namespace   *corev1.Namespace
		wantFailure bool
	}{
		{
			name: "namespace is privileged",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "privileged-ns",
					Annotations: map[string]string{
						"security.openshift.io/MinimallySufficientPodSecurityStandard": "privileged",
					},
				},
			},
		},
		{
			name: "namespace exists but annotations are nil",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "no-labels",
					Annotations: nil,
				},
			},
			wantFailure: true,
		},
		{
			name: "namespace exists but not privileged",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "restricted-ns",
					Annotations: map[string]string{
						"security.openshift.io/MinimallySufficientPodSecurityStandard": "restricted",
					},
				},
			},
			wantFailure: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatalf("add to scheme %s", err)
			}

			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.namespace).Build()
			err := WaitUntilNamespaceIsPrivileged(context.Background(), testNS, client, 1*time.Nanosecond, 1*time.Nanosecond)

			if tc.wantFailure && err == nil {
				t.Error("missed failure")
			}
		})
	}
}
