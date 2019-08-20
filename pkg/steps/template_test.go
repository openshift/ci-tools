package steps

import (
	"testing"

	coreapi "k8s.io/api/core/v1"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/diff"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	templateapi "github.com/openshift/api/template/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestInjectResourcesToPod(t *testing.T) {
	corev1Scheme := runtime.NewScheme()
	utilruntime.Must(coreapi.AddToScheme(corev1Scheme))
	corev1Codec := serializer.NewCodecFactory(corev1Scheme).LegacyCodec(coreapi.SchemeGroupVersion)

	utilruntime.Must(rbacv1.AddToScheme(corev1Scheme))
	rbacv1Codec := serializer.NewCodecFactory(corev1Scheme).LegacyCodec(rbacv1.SchemeGroupVersion)

	testCases := []struct {
		testID    string
		resources api.ResourceConfiguration
		template  *templateapi.Template
		expected  *templateapi.Template
	}{
		{
			testID: "no resource requests are defined, pod with container named 'test' exists but is not changed",
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
			testID: "resource requests are defined, pod has no container named 'test' and is not changed",
			resources: api.ResourceConfiguration{
				"test-template": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
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
								Containers: []coreapi.Container{{Name: "no-test"}},
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
								Containers: []coreapi.Container{{Name: "no-test"}},
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
			testID: "resource requests are defined, pod with container named 'test' has no resource requests and inherits resources from the config",
			resources: api.ResourceConfiguration{
				"test-template": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
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
								Containers: []coreapi.Container{{
									Name: "test",
									Resources: coreapi.ResourceRequirements{
										Requests: coreapi.ResourceList{
											"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
											"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
										Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
									}}},
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
			testID: "resource requests are defined, pod with container named 'test' has pre-existing resource requests and has those overridden by the config",
			resources: api.ResourceConfiguration{
				"test-template": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
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
								Containers: []coreapi.Container{{
									Name: "test",
									Resources: coreapi.ResourceRequirements{
										Requests: coreapi.ResourceList{
											"cpu":    *resource.NewQuantity(2, resource.DecimalSI),
											"memory": *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI)},
										Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(5*1024*1024*1024, resource.BinarySI)},
									}}},
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
								Containers: []coreapi.Container{{
									Name: "test",
									Resources: coreapi.ResourceRequirements{
										Requests: coreapi.ResourceList{
											"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
											"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
										Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
									}}},
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
			testID: "resource requests are defined, template contains no-pod objects, pod with container named 'test' exist and changed",
			resources: api.ResourceConfiguration{
				"test-template": {
					Requests: api.ResourceList{"cpu": "3", "memory": "8Gi"},
					Limits:   api.ResourceList{"memory": "10Gi"},
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
								Containers: []coreapi.Container{{
									Name: "test",
									Resources: coreapi.ResourceRequirements{
										Requests: coreapi.ResourceList{
											"cpu":    *resource.NewQuantity(2, resource.DecimalSI),
											"memory": *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI)},
										Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(5*1024*1024*1024, resource.BinarySI)},
									}}},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
					func() runtime.RawExtension {
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
								Containers: []coreapi.Container{{
									Name: "test",
									Resources: coreapi.ResourceRequirements{
										Requests: coreapi.ResourceList{
											"cpu":    *resource.NewQuantity(3, resource.DecimalSI),
											"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI)},
										Limits: coreapi.ResourceList{"memory": *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI)},
									}}},
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
					func() runtime.RawExtension {
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
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testID, func(t *testing.T) {
			template := tc.template
			expectedTemplate := tc.expected

			operateOnTemplatePods(template, tc.resources, "")
			if !equality.Semantic.DeepEqual(expectedTemplate, template) {
				t.Fatal(diff.ObjectDiff(expectedTemplate, template))
			}

		})
	}
}

func TestInjectLabelsToTemplate(t *testing.T) {
	corev1Scheme := runtime.NewScheme()
	utilruntime.Must(coreapi.AddToScheme(corev1Scheme))
	corev1Codec := serializer.NewCodecFactory(corev1Scheme).LegacyCodec(coreapi.SchemeGroupVersion)

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
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
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
				Objects: []runtime.RawExtension{
					func() runtime.RawExtension {
						pod := &coreapi.Pod{
							TypeMeta:   meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{Name: "test-pod"},
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
							TypeMeta: meta.TypeMeta{Kind: "Pod", APIVersion: "v1"},
							ObjectMeta: meta.ObjectMeta{
								Name: "test-pod",
							},
						}
						return runtime.RawExtension{
							Raw:    []byte(runtime.EncodeOrDie(corev1Codec, pod)),
							Object: pod.DeepCopyObject(),
						}
					}(),
				},
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
