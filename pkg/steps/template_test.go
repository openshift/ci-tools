package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetPodFromObject(t *testing.T) {
	testCases := []struct {
		testID string
		object runtime.RawExtension
	}{
		{
			testID: "empty object, expect nothing",
			object: runtime.RawExtension{},
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
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			pod := getPodFromObject(tc.object)
			testhelper.CompareWithFixture(t, pod)
		})
	}
}

func TestOperateOnTemplatePods(t *testing.T) {
	testCases := []struct {
		testID    string
		resources api.ResourceConfiguration
		template  *templateapi.Template
	}{
		{
			testID: "template with no pod, no changes expected",
			template: &templateapi.Template{
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
		},
		{
			testID: "template with pod with artifacts Volume/VolumeMount, changes expected",
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
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			operateOnTemplatePods(tc.template, tc.resources)
			testhelper.CompareWithFixture(t, tc.template)
		})
	}
}

func TestInjectResourcesToPod(t *testing.T) {
	testTemplateName := "test-template"
	testCases := []struct {
		testID    string
		resources api.ResourceConfiguration
		pod       *coreapi.Pod
	}{
		{
			testID: "no resource requests are defined, container named 'test' exists, expect no changes",
			pod: &coreapi.Pod{
				TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
				ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
				Spec:       coreapi.PodSpec{Containers: []coreapi.Container{{Name: "test"}}},
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
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			pod := tc.pod

			if err := injectResourcesToPod(pod, testTemplateName, tc.resources); err != nil {
				t.Fatalf("injectResourcesToPod failed: %v", err)
			}
			testhelper.CompareWithFixture(t, pod)
		})
	}
}

func TestInjectLabelsToTemplate(t *testing.T) {
	testCases := []struct {
		testID   string
		jobSpec  *api.JobSpec
		template *templateapi.Template
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
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			injectLabelsToTemplate(tc.jobSpec, tc.template)
			testhelper.CompareWithFixture(t, tc.template)
		})
	}
}
