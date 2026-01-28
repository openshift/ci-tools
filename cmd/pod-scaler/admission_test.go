package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
	"sigs.k8s.io/prow/pkg/kube"

	buildv1 "github.com/openshift/api/build/v1"
	fakebuildv1client "github.com/openshift/client-go/build/clientset/versioned/fake"

	"github.com/openshift/ci-tools/pkg/api"
	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type mockReporter struct {
	client *http.Client
	called bool
}

func (r *mockReporter) ReportResourceConfigurationWarning(string, string, string, string, string, bool, string) {
	r.called = true
}

var defaultReporter = mockReporter{client: &http.Client{}}

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
	decoder := admission.NewDecoder(scheme.Scheme)
	logger := logrus.WithField("test", t.Name())
	resources := &resourceServer{
		logger: logger,
		lock:   sync.RWMutex{},
		byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{
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
					corev1.ResourceCPU:    *resource.NewQuantity(9, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e4, resource.BinarySI),
				},
			},
		},
	}

	mutator := podMutator{
		logger:                logger,
		client:                client.BuildV1(),
		resources:             resources,
		mutateResourceLimits:  true,
		decoder:               decoder,
		cpuCap:                10,
		memoryCap:             "20Gi",
		cpuPriorityScheduling: 8,
		reporter:              &defaultReporter,
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
		{
			name: "pod marked with ignore annotation",
			request: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
					Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "name": "somethingelse","namespace": "namespace", "labels": {"openshift.io/build.name": "withlabels"}, "annotations": {"openshift.io/build.name": "withlabels", "ci-workload-autoscaler.openshift.io/scale": "false"}}, "spec":{"containers":[{"name":"test"},{"name":"other"}]}, "status":{}}`)},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response := mutator.Handle(context.Background(), testCase.request)
			testhelper.CompareWithFixture(t, response)
		})
	}
}

func TestMutatePodMetadata(t *testing.T) {
	var testCases = []struct {
		name          string
		pod           *corev1.Pod
		expected      *corev1.Pod
		expectedError error
	}{
		{
			name:     "not a rehearsal Pod",
			pod:      &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
		{
			name:          "rehearsal Pod with test container",
			pod:           &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}}},
			expectedError: errors.New("could not find test container in the rehearsal Pod"),
		},
		{
			name: "rehearsal Pod with no config env",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			},
			expectedError: errors.New("could not find $CONFIG_SPEC in the environment of the rehearsal Pod's test container"),
		},
		{
			name: "rehearsal Pod with malformed config",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "CONFIG_SPEC", Value: "nothing"}}}}},
			},
			expectedError: errors.New("could not unmarshal configuration from rehearsal pod: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type api.ReleaseBuildConfiguration"),
		},
		{
			name: "rehearsal Pod running something other than ci-op does not error",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "ENTRYPOINT_OPTIONS", Value: `{"args":["lol"]}`}}}}},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "ENTRYPOINT_OPTIONS", Value: `{"args":["lol"]}`}}}}},
			},
		},
		{
			name: "rehearsal Pod malformed entrypoint opts",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "ENTRYPOINT_OPTIONS", Value: "nothing"}}}}},
			},
			expectedError: errors.New("could not find $CONFIG_SPEC in the environment of the rehearsal Pod's test container, could not parse $ENTRYPOINT_OPTIONS: invalid character 'o' in literal null (expecting 'u')"),
		},
		{
			name: "rehearsal Pod with config",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{rehearse.Label: "1", rehearse.LabelContext: "context"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "CONFIG_SPEC", Value: `zz_generated_metadata:
  org: org
  repo: repo
  branch: branch`}}}}},
			},
			expected: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					rehearse.Label: "1", rehearse.LabelContext: "context",
					kube.ContextAnnotation: "context",
					kube.OrgLabel:          "org",
					kube.RepoLabel:         "repo",
					kube.BaseRefLabel:      "branch",
				}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "CONFIG_SPEC", Value: `zz_generated_metadata:
  org: org
  repo: repo
  branch: branch`}}}}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutatePodMetadata(testCase.pod, logrus.WithField("test", testCase.name))
			if diff := cmp.Diff(testCase.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: incorrect error: %v", testCase.name, diff)
			}
			if testCase.expectedError != nil {
				return
			}
			if diff := cmp.Diff(testCase.pod, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect pod after mutation: %v", testCase.name, diff)
			}
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
				"created-by-ci":                    "true",
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
				"created-by-ci":                    "true",
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
	metaBase := podscaler.FullMetadata{
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
	baseWithContainer := func(base *podscaler.FullMetadata, container string) podscaler.FullMetadata {
		copied := *base
		copied.Container = container
		return copied
	}

	var testCases = []struct {
		name                 string
		server               *resourceServer
		mutateResourceLimits bool
		pod                  *corev1.Pod
	}{
		{
			name: "no resources to add",
			server: &resourceServer{
				logger:     logger,
				lock:       sync.RWMutex{},
				byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{},
			},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			}}},
			mutateResourceLimits: true,
		},
		{
			name: "resources to add",
			server: &resourceServer{
				logger: logger,
				lock:   sync.RWMutex{},
				byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{
					baseWithContainer(&metaBase, "large"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e8, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "medium"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e8, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "small"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e8, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "overcap"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(20, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e8, resource.BinarySI),
						},
					},
				},
			},
			mutateResourceLimits: true,
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
									corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(3e8, resource.BinarySI),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(16, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(4e8, resource.BinarySI),
								},
							},
						},
						{
							Name: "medium", // we set larger CPU requirements, memory will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(1e8, resource.BinarySI),
								},
								Limits: corev1.ResourceList{},
							},
						},
						{
							Name: "small", // we set smaller requirements, these will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(1e2, resource.BinarySI),
								},
								Limits: corev1.ResourceList{},
							},
						},
						{
							Name: "small", // we set smaller cpu but recommendation is over cap, so we end up with the cap
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
		{
			name: "resources to add, limits disabled",
			server: &resourceServer{
				logger: logger,
				lock:   sync.RWMutex{},
				byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{
					baseWithContainer(&metaBase, "large"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "medium"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
					baseWithContainer(&metaBase, "small"): {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
						},
					},
				},
			},
			mutateResourceLimits: false,
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
									corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(16, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
								},
							},
						},
						{
							Name: "medium", // we set larger CPU requirements, memory will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(1e10, resource.BinarySI),
								},
								Limits: corev1.ResourceList{},
							},
						},
						{
							Name: "small", // we set smaller requirements, these will change
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
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
			mutatePodResources(testCase.pod, testCase.server, testCase.mutateResourceLimits, 10, "20Gi", false, nil, &defaultReporter, logrus.WithField("test", testCase.name))
			diff := cmp.Diff(original, testCase.pod)
			// In some cases, cmp.Diff decides to use non-breaking spaces, and it's not
			// particularly deterministic about this. We don't care.
			diff = strings.Map(func(r rune) rune {
				if r == '\u00a0' {
					return '\u0020'
				}
				return r
			}, diff)
			testhelper.CompareWithFixture(t, diff, testhelper.WithExtension(".diff"))
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
					corev1.ResourceCPU:    *resource.NewQuantity(240, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(36e9, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(120, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(24e9, resource.BinarySI),
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
					corev1.ResourceCPU:    *resource.NewQuantity(240, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(36e9, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(120, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(24e9, resource.BinarySI),
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
					corev1.ResourceCPU:    *resource.NewQuantity(480, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(1200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(48e9, resource.BinarySI),
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			useOursIfLarger(&testCase.ours, &testCase.theirs, "test", "build", false, "", &defaultReporter, logrus.WithField("test", testCase.name))
			if diff := cmp.Diff(testCase.theirs, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect resources after mutation: %v", testCase.name, diff)
			}
		})
	}
}

// TestUseOursIsLarger_ReporterReports tests the behaviour of useOursIsLarger
// when our determined resources are 10 times larger than the memory request.
func TestUseOursIsLarger_ReporterReports(t *testing.T) {
	var testCases = []struct {
		name         string
		ours, theirs corev1.ResourceRequirements
		reporter     mockReporter
		expected     bool
	}{
		{
			name: "ours is 10 times larger than theirs",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1, resource.BinarySI),
				},
			},
			reporter: mockReporter{client: &http.Client{}},
			expected: true,
		},
		{
			name: "ours is not 10 times larger than theirs",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			reporter: mockReporter{client: &http.Client{}},
			expected: false,
		},
		{
			//pod-scaler admission should not report the warning when their configured memory is 0
			name: "ours is 10x larger, their memory is 0",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(3e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
				},
			},
			reporter: mockReporter{client: &http.Client{}},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			useOursIfLarger(&tc.ours, &tc.theirs, "test", "build", false, "", &tc.reporter, logrus.WithField("test", tc.name))

			if diff := cmp.Diff(tc.reporter.called, tc.expected); diff != "" {
				t.Errorf("actual and expected reporter states don't match, : %v", diff)
			}
		})
	}
}

func TestReconcileLimits(t *testing.T) {
	var testCases = []struct {
		name            string
		input, expected corev1.ResourceRequirements
	}{
		{
			name: "nothing to do",
		},
		{
			name: "remove CPU limits",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(200, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
			},
		},
		{
			name: "increase low memory limits",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "do nothing for adequate memory limits",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(4e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "do nothing when no memory limits have been configured",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			reconcileLimits(&testCase.input)
			if diff := cmp.Diff(testCase.input, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect resources after limit reconciliation: %v", testCase.name, diff)
			}
		})
	}
}

func TestRehearsalMetadata(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "d316d4cc-a437-11eb-b35f-0a580a800e92", Labels: map[string]string{
			rehearse.Label:              "1",
			rehearse.LabelContext:       "context",
			"created-by-prow":           "true",
			"prow.k8s.io/refs.org":      "org",
			"prow.k8s.io/refs.repo":     "repo",
			"prow.k8s.io/refs.base_ref": "branch",
			"prow.k8s.io/context":       "rehearse-context",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Env: []corev1.EnvVar{{Name: "CONFIG_SPEC", Value: `zz_generated_metadata:
  org: ORG
  repo: REPO
  branch: BRANCH`}}}}},
	}
	meta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:    "ORG",
			Repo:   "REPO",
			Branch: "BRANCH",
		},
		Target:    "context",
		Container: "test",
	}
	if err := mutatePodMetadata(pod, logrus.WithField("test", "TestRehearsalMetadata")); err != nil {
		t.Fatalf("failed to mutate metadata: %v", err)
	}
	if diff := cmp.Diff(podscaler.MetadataFor(pod.ObjectMeta.Labels, pod.ObjectMeta.Name, "test"), meta); diff != "" {
		t.Errorf("rehearsal job: got incorrect metadata: %v", diff)
	}
}

func TestPreventUnschedulable(t *testing.T) {
	cpuCap := int64(10)
	memoryCap := "20Gi"
	testCases := []struct {
		name      string
		resources *corev1.ResourceRequirements
		expected  *corev1.ResourceRequirements
	}{
		{
			name: "valid CPU and memory",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(9, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			expected: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(9, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
		{
			name: "too much CPU",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(cpuCap, resource.DecimalSI),
				},
			},
		},
		{
			name: "too much memory",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("25Gi"),
				},
			},
			expected: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse(memoryCap),
				},
			},
		},
		{
			name: "too much CPU and memory",
			resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse("25Gi"),
				},
			},
			expected: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(cpuCap, resource.DecimalSI),
					corev1.ResourceMemory: resource.MustParse(memoryCap),
				},
			},
		},
		{
			name:      "no requests",
			resources: &corev1.ResourceRequirements{},
			expected:  &corev1.ResourceRequirements{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preventUnschedulable(tc.resources, cpuCap, memoryCap, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.expected, tc.resources); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestDetermineWorkloadType(t *testing.T) {
	testCases := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		expected    string
	}{
		{
			name:        "no labels or annotations",
			annotations: map[string]string{},
			labels:      map[string]string{},
			expected:    WorkloadTypeUndefined,
		},
		{
			name:        "build pod",
			annotations: map[string]string{buildv1.BuildLabel: "buildName"},
			expected:    WorkloadTypeBuild,
		},
		{
			name:     "prowjob",
			labels:   map[string]string{"prow.k8s.io/job": "jobName"},
			expected: WorkloadTypeProwjob,
		},
		{
			name:     "step",
			labels:   map[string]string{steps.LabelMetadataStep: "e2e"},
			expected: WorkloadTypeStep,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(determineWorkloadType(tc.annotations, tc.labels), tc.expected); diff != "" {
				t.Errorf("result differs from expected output, diff:\n%s", diff)
			}
		})
	}
}

func TestDetermineWorkloadName(t *testing.T) {
	testCases := []struct {
		name         string
		workloadType string
		labels       map[string]string
		expected     string
	}{
		{
			name:         "workload is prowjob",
			workloadType: WorkloadTypeProwjob,
			labels:       map[string]string{"prow.k8s.io/job": "prowjobName"},
			expected:     "prowjobName",
		},
		{
			name:         "workload is a step",
			workloadType: WorkloadTypeStep,
			labels:       nil,
			expected:     "pod-container",
		},
		{
			name:         "workload is a build",
			workloadType: WorkloadTypeBuild,
			labels:       nil,
			expected:     "pod-container",
		},
		{
			name:         "workload type is undefined",
			workloadType: WorkloadTypeUndefined,
			labels:       nil,
			expected:     "pod-container",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(determineWorkloadName("pod", "container", tc.workloadType, tc.labels), tc.expected); diff != "" {
				t.Errorf("result differs from expected output, diff:\n%s", diff)
			}
		})
	}
}

func TestAddPriorityClass(t *testing.T) {
	priority := new(int32)
	*priority = 10
	preemptionPolicy := corev1.PreemptLowerPriority

	testCases := []struct {
		name     string
		pod      *corev1.Pod
		expected *corev1.Pod
	}{
		{
			name: "cpu under configured amount for priority scheduling",
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}},
				},
			}},
			expected: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}},
				},
			}},
		},
		{
			name: "cpu of exactly configured amount for priority scheduling",
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}}},
				},
			}},
			expected: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}}},
				},
				PriorityClassName: priorityClassName,
			}},
		},
		{
			name: "cpu above configured amount for priority scheduling",
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
			}},
			expected: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
				PriorityClassName: priorityClassName,
			}},
		},
		{
			name: "cpu above configured amount for priority scheduling with priority and preemption policy defined",
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
				Priority:         priority,
				PreemptionPolicy: &preemptionPolicy,
			}},
			expected: &corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
				PriorityClassName: priorityClassName,
			}},
		},
		{
			name: "cpu of initContainer above configured amount for priority scheduling",
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
			}},
			expected: &corev1.Pod{Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("9")}}},
				},
				PriorityClassName: priorityClassName,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := podMutator{cpuPriorityScheduling: 8}
			m.addPriorityClass(tc.pod)
			if diff := cmp.Diff(tc.pod, tc.expected); diff != "" {
				t.Fatalf("expected pod doesn't match actual, diff: %s", diff)
			}
		})
	}
}
