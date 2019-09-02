package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestGetPodFromObject(t *testing.T) {
	testCases := []struct {
		testID      string
		object      runtime.RawExtension
		expectedPod *coreapi.Pod
	}{
		{
			testID: "empty object, expect nothing",
			object: runtime.RawExtension{},
		},
		{
			testID: "object different than pod, expect nothing",
			object: func() runtime.RawExtension {
				rolebinding := &rbacv1.RoleBinding{
					ObjectMeta: meta.ObjectMeta{
						Name:      "image-puller",
						Namespace: "test-namespace",
					},
				}

				return runtime.RawExtension{
					Raw:    []byte(runtime.EncodeOrDie(rbacv1Codec, rolebinding)),
					Object: rolebinding.DeepCopyObject(),
				}
			}(),
		},
		{
			testID: "object is a Pod, expect to get a pod struct",
			object: func() runtime.RawExtension {
				pod := &coreapi.Pod{
					TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
					ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
					Spec: coreapi.PodSpec{
						Containers: []coreapi.Container{{Name: "test"}},
					},
				}
				return runtime.RawExtension{
					Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
					Object: pod.DeepCopyObject(),
				}
			}(),
			expectedPod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			pod := getPodFromObject(tc.object)
			if !equality.Semantic.DeepEqual(pod, tc.expectedPod) {
				t.Fatal(diff.ObjectReflectDiff(pod, tc.expectedPod))
			}
		})
	}
}
