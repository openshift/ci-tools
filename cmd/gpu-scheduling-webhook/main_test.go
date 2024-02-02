package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestMutatePod(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		pod     runtime.Object
		wantPod corev1.Pod
		wantErr error
	}{
		{
			name: "Request a GPU therefore add toleration",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "c1",
							Command: []string{"cmd"},
							Image:   "img",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			wantPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "c1",
							Command: []string{"cmd"},
							Image:   "img",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{nvidiaGPUToleration},
				},
			},
		},
		{
			name: "No GPU request a GPU, leave pod untouched",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:    "c1",
					Command: []string{"cmd"},
					Image:   "img",
				}}},
			},
			wantPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:    "c1",
					Command: []string{"cmd"},
					Image:   "img",
				}}},
			},
		},
		{
			name: "Do not add the same toleration again",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "c1",
							Command: []string{"cmd"},
							Image:   "img",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{nvidiaGPUToleration},
				},
			},
			wantPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "c1",
							Command: []string{"cmd"},
							Image:   "img",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									nvidiaGPU: resource.MustParse("1"),
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{nvidiaGPUToleration},
				},
			},
		},
		{
			name:    "Not a pod, return an error",
			pod:     &corev1.Node{},
			wantErr: errors.New("expected a Pod but got a *v1.Node"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pgs := gpuTolerator{}
			err := pgs.Default(context.TODO(), testCase.pod)

			if err != nil && testCase.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && testCase.wantErr != nil {
				t.Fatalf("want err %v but nil", testCase.wantErr)
			}
			if err != nil && testCase.wantErr != nil {
				if diff := cmp.Diff(testCase.wantErr.Error(), err.Error()); diff != "" {
					t.Fatalf("unexpected error: %s", diff)
				}
				return
			}

			pod, _ := testCase.pod.(*corev1.Pod)
			if diff := cmp.Diff(testCase.wantPod, *pod); diff != "" {
				t.Error(diff)
			}
		})
	}
}
