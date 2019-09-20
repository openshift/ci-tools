package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
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
		resources    api.ResourceConfiguration
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
		{
			testID: "resources defined in the config but the test container has existing resources, no changes expected",
			resources: api.ResourceConfiguration{
				"*": {
					Requests: api.ResourceList{"cpu": "1", "memory": "2Gi"},
					Limits:   api.ResourceList{"memory": "3Gi"},
				},
				"test-template": {
					Requests: api.ResourceList{"cpu": "5", "memory": "10Gi"},
					Limits:   api.ResourceList{"memory": "16Gi"},
				},
			},
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Containers: []coreapi.Container{
									{
										Name: "test",
										Resources: coreapi.ResourceRequirements{
											Requests: coreapi.ResourceList{
												"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
												"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
											Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
										},
									},
								}},
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
								Containers: []coreapi.Container{
									{
										Name: "test",
										Resources: coreapi.ResourceRequirements{
											Requests: coreapi.ResourceList{
												"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
												"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
											Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
										},
									},
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
		{
			testID: "resources defined in the config,no existing resources in the container, changes are expected",
			resources: api.ResourceConfiguration{
				"*": {
					Requests: api.ResourceList{"cpu": "1", "memory": "2Gi"},
					Limits:   api.ResourceList{"memory": "3Gi"},
				},
				"test-template": {
					Requests: api.ResourceList{"cpu": "5", "memory": "10Gi"},
					Limits:   api.ResourceList{"memory": "16Gi"},
				},
			},
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Containers: []coreapi.Container{{Name: "test"}}},
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
								Containers: []coreapi.Container{
									{
										Name: "test",
										Resources: coreapi.ResourceRequirements{
											Requests: coreapi.ResourceList{
												"cpu":    *resource.NewQuantity(5, resource.DecimalSI),
												"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
											Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI)},
										},
									},
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
		{
			testID: "only default resources defined in the config,no existing resources in the container, changes are expected",
			resources: api.ResourceConfiguration{
				"*": {
					Requests: api.ResourceList{"cpu": "1", "memory": "2Gi"},
					Limits:   api.ResourceList{"memory": "3Gi"},
				},
			},
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
							Spec: coreapi.PodSpec{
								Containers: []coreapi.Container{{Name: "test"}}},
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
								Containers: []coreapi.Container{
									{
										Name: "test",
										Resources: coreapi.ResourceRequirements{
											Requests: coreapi.ResourceList{
												"cpu":    *resource.NewQuantity(1, resource.DecimalSI),
												"memory": *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI)},
											Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI)},
										},
									},
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
			operateOnTemplatePods(tc.template, tc.artifactsDir, tc.resources)
			if !equality.Semantic.DeepEqual(tc.template, tc.expected) {
				t.Fatal(diff.ObjectReflectDiff(tc.template, tc.expected))
			}

		})
	}
}

func TestInjectResourcesToPod(t *testing.T) {
	testTemplateName := "test-template"
	testCases := []struct {
		testID    string
		resources api.ResourceConfiguration
		pod       *coreapi.Pod
		expected  *coreapi.Pod
	}{
		{
			testID: "no resource requests are defined, container named 'test' exists, expect no changes",
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec:       coreapi.PodSpec{Containers: []coreapi.Container{{Name: "test"}}},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
		},
		{
			testID: "resource requests are defined, no container named 'test' exists, expect no changes",
			resources: api.ResourceConfiguration{
				testTemplateName: {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
				},
			},
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "no-test"}},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "no-test"}},
				},
			},
		},

		{
			testID: "both default and template's resource requests are defined, pod has container named 'test', and is changed",
			resources: api.ResourceConfiguration{
				"*": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
				},
				"test-template": {
					Requests: api.ResourceList{"cpu": "5", "memory": "10Gi"},
					Limits:   api.ResourceList{"memory": "16Gi"},
				},
			},
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Name: "test",
							Resources: coreapi.ResourceRequirements{
								Requests: coreapi.ResourceList{
									"cpu":    *resource.NewQuantity(5, resource.DecimalSI),
									"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
								Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI)},
							},
						},
					},
				},
			},
		},

		{
			testID: "only the default resource requests are defined, pod has container named 'test', and is changed",
			resources: api.ResourceConfiguration{
				"*": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
				},
			},
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{Name: "test"}},
				},
			},
			expected: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Name: "test",
							Resources: coreapi.ResourceRequirements{
								Requests: coreapi.ResourceList{
									"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
									"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
								Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			pod := tc.pod
			expectedPod := tc.expected

			injectResourcesToPod(pod, testTemplateName, tc.resources)
			if !equality.Semantic.DeepEqual(expectedPod, pod) {
				t.Fatal(diff.ObjectDiff(expectedPod, pod))
			}
		})
	}
}

func TestInjectLabelsToTemplate(t *testing.T) {
	testCases := []struct {
		testID   string
		jobSpec  *api.JobSpec
		template *templateapi.Template
		expected *templateapi.Template
	}{
		{
			testID: "nil refs in jobspec, no injection expected",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: nil,
				},
			},
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
			testID: "jobspec with refs, label injection expected",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:     "test-org",
						Repo:    "test-repo",
						BaseRef: "test-branch",
					},
				},
			},
			template: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
			},
			expected: &templateapi.Template{
				TypeMeta:   meta.TypeMeta{Kind: "Template", APIVersion: "template.openshift.io/v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-template"},
				ObjectLabels: map[string]string{
					"ci.openshift.io/refs.org":    "test-org",
					"ci.openshift.io/refs.repo":   "test-repo",
					"ci.openshift.io/refs.branch": "test-branch",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			injectLabelsToTemplate(tc.jobSpec, tc.template)
			if !equality.Semantic.DeepEqual(tc.expected, tc.template) {
				t.Fatal(diff.ObjectDiff(tc.expected, tc.template))
			}
		})
	}
}
