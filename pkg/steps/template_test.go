package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"

	templateapi "github.com/openshift/api/template/v1"
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

func TestOperateOnTemplatePods(t *testing.T) {
	testCases := []struct {
		testID       string
		artifactsDir string
		template     *templateapi.Template
		expected     *templateapi.Template
	}{
		{
			testID: "template with no pod, no changes expected",
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
			},
			expected: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
			},
		},
		{
			testID: "template with pod but with no artifacts Volume/VolumeMount, no changes expected",
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
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
				},
			},
			expected: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
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
				},
			},
		},
		{
			testID: "template with pod with artifacts Volume/VolumeMount but not artifacts dir defined, no changes expected",
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Volumes:    []coreapi.Volume{{Name: "artifacts"}},
								Containers: []coreapi.Container{{Name: "test", VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}}}},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
				},
			},
			expected: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Volumes:    []coreapi.Volume{{Name: "artifacts"}},
								Containers: []coreapi.Container{{Name: "test", VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}}}},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
				},
			},
		},
		{
			testID:       "template with pod with artifacts Volume/VolumeMount and artifacts dir defined, changes expected",
			artifactsDir: "/path/to/artifacts",
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Volumes:    []coreapi.Volume{{Name: "artifacts"}},
								Containers: []coreapi.Container{{Name: "test", VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}}}},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
				},
			},
			expected: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Volumes: []coreapi.Volume{{Name: "artifacts"}},
								Containers: []coreapi.Container{
									{
										Name: "test", VolumeMounts: []coreapi.VolumeMount{{Name: "artifacts"}},
									},
									testArtifactsContainer,
								},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			operateOnTemplatePods(tc.template, tc.artifactsDir)
			if !equality.Semantic.DeepEqual(tc.template, tc.expected) {
				t.Fatal(diff.ObjectReflectDiff(tc.template, tc.expected))
			}

		})
	}
}
