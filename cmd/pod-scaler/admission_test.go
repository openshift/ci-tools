package main

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	buildv1 "github.com/openshift/api/build/v1"
	fakebuildv1client "github.com/openshift/client-go/build/clientset/versioned/fake"

	"github.com/openshift/ci-tools/pkg/api"
	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestMutatePods(t *testing.T) {
	client := fakebuildv1client.NewSimpleClientset(
		&buildv1.Build{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Build",
				APIVersion: "build.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "namespace",
				Name:      "withoutlabels",
				Labels:    map[string]string{},
			},
		},
		&buildv1.Build{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Build",
				APIVersion: "build.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "namespace",
				Name:      "withlabels",
				Labels: map[string]string{
					"ci.openshift.io/metadata.org":     "org",
					"ci.openshift.io/metadata.repo":    "repo",
					"ci.openshift.io/metadata.branch":  "branch",
					"ci.openshift.io/metadata.variant": "variant",
					"ci.openshift.io/metadata.target":  "target",
				},
			},
		},
	)
	decoder, err := admission.NewDecoder(scheme.Scheme)
	if err != nil {
		t.Fatalf("failed to create decoder from scheme: %v", err)
	}
	logger := logrus.WithField("test", t.Name())
	resources := &resourceServer{
		logger: logger,
		lock:   sync.RWMutex{},
		byMetaData: map[pod_scaler.FullMetadata]corev1.ResourceRequirements{
			{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "withlabels-build",
				Container: "test",
			}: {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
	}
	mutator := podMutator{
		logger:    logger,
		client:    client.BuildV1(),
		decoder:   decoder,
		resources: resources,
	}

	var testCases = []struct {
		name    string
		request admission.Request
	}{
		{
			name: "not a pod",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
					Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
					Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Secret","metadata": {"name": "somethingelse","namespace": "namespace"}}`)},
				},
			},
		},
		{
			name: "pod not associated with a build",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
					Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "name": "somethingelse","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
				},
			},
		},
		{
			name: "pod associated with a build that has no labels",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
					Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withoutlabels"}, "annotations": {"openshift.io/build.name": "withoutlabels"}, "name": "withoutlabels-build","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
				},
			},
		},
		{
			name: "pod associated with a build with labels",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
					Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withlabels"}, "annotations": {"openshift.io/build.name": "withlabels"}, "name": "withlabels-build","namespace": "namespace"}, "spec":{"containers":[{"name":"test"},{"name":"other"}]}, "status":{}}`)},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := mutator.Handle(context.Background(), testCase.request)
			sort.Slice(response.Patches, func(i, j int) bool {
				return response.Patches[i].Path < response.Patches[j].Path
			})
			testhelper.CompareWithFixture(t, response)
		})
	}
}

func TestMutatePodLabels(t *testing.T) {
	var testCases = []struct {
		name     string
		build    *buildv1.Build
		pod      *corev1.Pod
		expected *corev1.Pod
	}{
		{
			name:     "no labels to add",
			build:    &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
			pod:      &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
		{
			name: "many labels to add",
			build: &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
		},
		{
			name: "some labels to add",
			build: &buildv1.Build{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":  "org",
				"ci.openshift.io/metadata.repo": "repo",
			}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mutatePodLabels(testCase.pod, testCase.build)
			if diff := cmp.Diff(testCase.pod, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect pod after mutation: %v", testCase.name, diff)
			}
		})
	}
}

func TestMutatePodResources(t *testing.T) {
	logger := logrus.WithField("test", t.Name())
	metaBase := pod_scaler.FullMetadata{
		Metadata: api.Metadata{
			Org:     "org",
			Repo:    "repo",
			Branch:  "branch",
			Variant: "variant",
		},
		Target: "target",
		Step:   "step",
		Pod:    "tomutate",
	}
	baseWithContainer := func(base *pod_scaler.FullMetadata, container string) pod_scaler.FullMetadata {
		copied := *base
		copied.Container = container
		return copied
	}

	var testCases = []struct {
		name   string
		server *resourceServer
		pod    *corev1.Pod
	}{
		{
			name: "no resources to add",
			server: &resourceServer{
				logger:     logger,
				lock:       sync.RWMutex{},
				byMetaData: map[pod_scaler.FullMetadata]corev1.ResourceRequirements{},
			},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
		},
		{
			name: "resources to add",
			server: &resourceServer{
				logger: logger,
				lock:   sync.RWMutex{},
				byMetaData: map[pod_scaler.FullMetadata]corev1.ResourceRequirements{
					baseWithContainer(&metaBase, "large"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "medium"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "small"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tomutate",
					Labels: map[string]string{
						"ci.openshift.io/metadata.org":     "org",
						"ci.openshift.io/metadata.repo":    "repo",
						"ci.openshift.io/metadata.branch":  "branch",
						"ci.openshift.io/metadata.variant": "variant",
						"ci.openshift.io/metadata.target":  "target",
						"ci.openshift.io/metadata.step":    "step",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "large", // we set larger requirements, these will not change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(400, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
								},
							},
						},
						{
							Name: "medium", // we set larger CPU requirements, memory will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(1e10, resource.BinarySI),
								},
								Limits: corev1.ResourceList{},
							},
						},
						{
							Name: "small", // we set smaller requirements, these will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(1e2, resource.BinarySI),
								},
								Limits: corev1.ResourceList{},
							},
						},
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			original := testCase.pod.DeepCopy()
			mutatePodResources(testCase.pod, testCase.server)
			diff := cmp.Diff(original, testCase.pod)
			testhelper.CompareWithFixture(t, diff)
		})
	}
}

func TestUseOursIfLarger(t *testing.T) {
	var testCases = []struct {
		name                   string
		ours, theirs, expected corev1.ResourceRequirements
	}{
		{
			name: "nothing in either",
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			},
		},
		{
			name: "nothing in theirs",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "nothing in ours",
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "nothing in theirs is larger",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "nothing in ours is larger",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "some things in ours are larger",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			useOursIfLarger(&testCase.ours, &testCase.theirs)
			if diff := cmp.Diff(testCase.theirs, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect resources after mutation: %v", testCase.name, diff)
			}
		})
	}
}
