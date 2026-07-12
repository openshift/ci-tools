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
	"k8s.io/apimachinery/pkg/util/sets"
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

func authPair(apply bool, maxReduction float64) authoritativePair {
	return authoritativePair{apply: apply, maxReduction: maxReduction}
}

func authLegacyCPU(maxReduction float64) authoritativeConfig {
	p := authPair(true, maxReduction)
	return authoritativeConfig{cpuRequest: p, cpuLimit: p}
}

func authLegacyCPUAndMemory(maxCPU, maxMemory float64) authoritativeConfig {
	return authoritativeConfig{
		cpuRequest:    authPair(true, maxCPU),
		cpuLimit:      authPair(true, maxCPU),
		memoryRequest: authPair(true, maxMemory),
		memoryLimit:   authPair(true, maxMemory),
	}
}

func authLegacyCPUDryRun(maxReduction float64) authoritativeConfig {
	p := authPair(false, maxReduction)
	return authoritativeConfig{cpuRequest: p, cpuLimit: p}
}

func authLegacyMemory(maxReduction float64) authoritativeConfig {
	p := authPair(true, maxReduction)
	return authoritativeConfig{memoryRequest: p, memoryLimit: p}
}

type mockReporter struct {
	client *http.Client
	called bool
}

func (r *mockReporter) ReportResourceConfigurationWarning(string, string, string, string, string, bool, string) {
	r.called = true
}

var defaultReporter = mockReporter{client: &http.Client{}}

var (
	testClusterCPUCap    = *resource.NewQuantity(10, resource.DecimalSI)
	testClusterMemoryCap = resource.MustParse("20Gi")
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
			mutatePodResources(testCase.pod, testCase.server, testCase.mutateResourceLimits, 10, "20Gi", false, nil, 50.0, authoritativeConfig{}, authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, nil, &defaultReporter, logrus.WithField("test", testCase.name))
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

func TestMutatePodResources_ciWorkloadLabelDoesNotBreakCacheLookup(t *testing.T) {
	logger := logrus.WithField("test", t.Name())
	cacheMeta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:     "org",
			Repo:    "repo",
			Branch:  "branch",
			Variant: "variant",
		},
		Target:    "target",
		Step:      "step",
		Pod:       "tomutate",
		Container: "test",
	}
	server := &resourceServer{
		logger: logger,
		lock:   sync.RWMutex{},
		byMetaData: map[podscaler.FullMetadata]corev1.ResourceRequirements{
			cacheMeta: {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewMilliQuantity(5000, resource.DecimalSI),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tomutate",
			Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"ci.openshift.io/metadata.step":    "step",
				"ci-workload":                      "prowjobs",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "test",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
					},
				},
			}},
		},
	}

	mutatePodResources(pod, server, false, 10, "20Gi", false, nil, 50.0, authoritativeConfig{}, authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, nil, &defaultReporter, logger)

	got := pod.Spec.Containers[0].Resources.Requests.Cpu().MilliValue()
	const want = 1000 // 10x cap from 100m configured; raw 5000m*1.2 would exceed threshold
	if got != want {
		t.Fatalf("CPU request = %dm, want %dm", got, want)
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
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
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
			name: "modest increase applied when below threshold",
			ours: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(200, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(3e9, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e9, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(180, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(25e8, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(90, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(15e8, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(240, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(36e8, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(120, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(24e8, resource.BinarySI),
				},
			},
		},
		{
			name: "excessive increase capped",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e2, resource.BinarySI),
				},
				Limits: corev1.ResourceList{},
			},
		},
		{
			name: "corrupt spike capped at cluster maximum",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(500*1024*1024*1024, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: testClusterMemoryCap,
				},
				Limits: corev1.ResourceList{},
			},
		},
		{
			name: "increase below cgroup minimum skipped",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(200000, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e6, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e6, resource.BinarySI),
				},
				Limits: corev1.ResourceList{},
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
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(48e9, resource.BinarySI),
				},
			},
		},
		{
			name: "unset configured resources are not mutated",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(270000, resource.BinarySI),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(270000, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			useOursIfLarger(&testCase.ours, &testCase.theirs, "test", "build", false, "", testClusterCPUCap, testClusterMemoryCap, &defaultReporter, logrus.WithField("test", testCase.name))
			if diff := cmp.Diff(testCase.theirs, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect resources after mutation: %v", testCase.name, diff)
			}
		})
	}
}

func TestUseOursIfLarger_authoritative(t *testing.T) {
	testCases := []struct {
		name                   string
		isMeasured             bool
		ours, theirs, expected corev1.ResourceRequirements
	}{
		{
			name: "authoritative disabled",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "smaller recommendation does not decrease requests",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name: "cpu decrease never below 10m",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1m"),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("12m"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("12m"),
				},
			},
		},
		{
			name: "zero cpu recommendation ignored",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.Quantity{},
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
		{
			name:       "measured pod skips reduction",
			isMeasured: true,
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			useOursIfLarger(&tc.ours, &tc.theirs, "test", "build", tc.isMeasured, "", testClusterCPUCap, testClusterMemoryCap, &defaultReporter, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.theirs, tc.expected); diff != "" {
				t.Errorf("unexpected resources: %s", diff)
			}
		})
	}
}

func TestApplyAuthoritativeLimitDecrease(t *testing.T) {
	testCases := []struct {
		name                   string
		authoritative          authoritativeConfig
		isMeasured             bool
		usageBasis             authoritativeDecreaseUsageBasis
		ours, theirs, expected corev1.ResourceRequirements
	}{
		{
			name: "authoritative disabled",
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
		},
		{
			name:          "capped reduction on requests and limits",
			authoritative: authLegacyCPUAndMemory(0.25, 0.25),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(75, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(15e9, resource.BinarySI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(75, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(15e9, resource.BinarySI),
				},
			},
		},
		{
			name:          "cpu limit decrease never below 10m",
			authoritative: authLegacyCPU(1.0),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1m"),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("12m"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("10m"),
				},
			},
		},
		{
			name:          "measured pod skips reduction",
			authoritative: authLegacyCPU(0.25),
			isMeasured:    true,
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
		},
		{
			name:          "capped reduction on requests without limits",
			authoritative: authLegacyCPU(0.25),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(75, resource.DecimalSI),
				},
			},
		},
		{
			name:          "peak basis skips decrease when spike exceeds configured",
			authoritative: authLegacyMemory(0.25),
			usageBasis:    authoritativeDecreaseUsagePeak,
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("7Gi"),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
		{
			name:          "memory limit decrease never below usable minimum",
			authoritative: authLegacyMemory(1.0),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(100, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("2Mi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit.DeepCopy(),
				},
			},
		},
		{
			name:          "memory request decrease never below usable minimum",
			authoritative: authLegacyMemory(1.0),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(100, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("2Mi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit.DeepCopy(),
				},
			},
		},
		{
			name:          "memory limit exactly at usable minimum stays unchanged",
			authoritative: authLegacyMemory(1.0),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(100, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit.DeepCopy(),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit.DeepCopy(),
				},
			},
		},
		{
			name:          "normal memory limit decrease is unaffected by usable memory floor",
			authoritative: authLegacyCPUAndMemory(0.25, 0.25),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(128e6, resource.BinarySI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(256e6, resource.BinarySI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(75, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(192e6, resource.BinarySI),
				},
			},
		},
		{
			name:          "cpu decrease unchanged by usable memory floor",
			authoritative: authLegacyCPU(0.25),
			ours: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
				},
			},
			theirs: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(75, resource.DecimalSI),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			usageBasis := tc.usageBasis
			if usageBasis == "" {
				usageBasis = authoritativeDecreaseUsageP80
			}
			applyAuthoritativeLimitDecrease(&tc.ours, &tc.theirs, "test", WorkloadTypeProwjob, tc.isMeasured, "", tc.authoritative, usageBasis, authoritativeSkipConfig{}, nil, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.theirs, tc.expected); diff != "" {
				t.Errorf("unexpected resources: %s", diff)
			}
		})
	}
}

func TestApplyAuthoritativeLimitDecrease_uncappedMemory(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(10, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(100, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(75, resource.DecimalSI),
			corev1.ResourceMemory: authoritativeMinMemoryLimit.DeepCopy(),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test", WorkloadTypeProwjob, false, "", authLegacyCPUAndMemory(0.25, 1.0), authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("unexpected resources: %s", diff)
	}
}

func TestApplyAuthoritativeLimitDecrease_dryRun(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test", WorkloadTypeProwjob, false, "", authLegacyCPUDryRun(0.25), authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("dry-run should not mutate resources: %s", diff)
	}
}

func TestApplyAuthoritativeLimitDecrease_separateRequestLimitCaps(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(12, resource.DecimalSI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(75, resource.DecimalSI),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test", WorkloadTypeProwjob, false, "", authoritativeConfig{
		cpuRequest: authPair(true, 0.25),
		cpuLimit:   authPair(true, 1.0),
	}, authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("unexpected resources: %s", diff)
	}
}

func TestClampRequestsToLimits(t *testing.T) {
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	expected := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	clampRequestsToLimits(&resources)
	if diff := cmp.Diff(resources, expected); diff != "" {
		t.Errorf("unexpected resources: %s", diff)
	}
}

func TestUsageForAuthoritativeDecrease(t *testing.T) {
	testCases := []struct {
		name     string
		basis    authoritativeDecreaseUsageBasis
		resource corev1.ResourceName
		request  string
		peak     string
		want     string
		wantOK   bool
	}{
		{
			name:     "peak basis uses request when peak is stale low",
			basis:    authoritativeDecreaseUsagePeak,
			resource: corev1.ResourceMemory,
			request:  "2Gi",
			peak:     "100Mi",
			want:     "2Gi",
			wantOK:   true,
		},
		{
			name:     "peak basis uses peak when higher",
			basis:    authoritativeDecreaseUsagePeak,
			resource: corev1.ResourceMemory,
			request:  "1Gi",
			peak:     "3Gi",
			want:     "3Gi",
			wantOK:   true,
		},
		{
			name:     "p80 basis uses request",
			basis:    authoritativeDecreaseUsageP80,
			resource: corev1.ResourceMemory,
			request:  "1500Mi",
			peak:     "3Gi",
			want:     "1500Mi",
			wantOK:   true,
		},
		{
			name:     "cpu peak basis uses request when peak is stale low",
			basis:    authoritativeDecreaseUsagePeak,
			resource: corev1.ResourceCPU,
			request:  "2",
			peak:     "500m",
			want:     "2",
			wantOK:   true,
		},
		{
			name:   "missing usage",
			basis:  authoritativeDecreaseUsageP80,
			wantOK: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recommended := corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			}
			if tc.request != "" {
				recommended.Requests[tc.resource] = resource.MustParse(tc.request)
			}
			if tc.peak != "" {
				recommended.Limits[tc.resource] = resource.MustParse(tc.peak)
			}
			got, ok := usageForAuthoritativeDecrease(recommended, tc.resource, tc.basis)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			want := resource.MustParse(tc.want)
			if got.Cmp(want) != 0 {
				t.Fatalf("usageForAuthoritativeDecrease() = %s, want %s", got.String(), want.String())
			}
		})
	}
}

func TestSanitizeCorruptConfiguredResources(t *testing.T) {
	authoritativeApply := authLegacyCPUAndMemory(1.0, 1.0)
	authoritativeDryRun := authoritativeConfig{
		cpuRequest:    authPair(false, 1.0),
		cpuLimit:      authPair(false, 1.0),
		memoryRequest: authPair(false, 1.0),
		memoryLimit:   authPair(false, 1.0),
	}
	testCases := []struct {
		name                              string
		authoritative                     authoritativeConfig
		configured, recommended, expected corev1.ResourceRequirements
	}{
		{
			name:          "sub-minimum memory request raised",
			authoritative: authoritativeApply,
			configured: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("260Ki"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("512Ki"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit,
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: authoritativeMinMemoryLimit,
				},
			},
		},
		{
			name:          "10x recommendation clamp",
			authoritative: authoritativeApply,
			configured: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Gi"),
				},
			},
			recommended: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("10Gi"),
				},
			},
		},
		{
			name:          "cluster memory cap clamp",
			authoritative: authoritativeApply,
			configured: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("25Gi"),
				},
			},
			recommended: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: testClusterMemoryCap,
				},
			},
		},
		{
			name:          "cluster cpu cap clamp",
			authoritative: authoritativeApply,
			configured: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100"),
				},
			},
			recommended: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: testClusterCPUCap,
				},
			},
		},
		{
			name:          "dry-run skips downscale from tiny recommendation",
			authoritative: authoritativeDryRun,
			configured: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			recommended: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("1Mi"),
				},
			},
			expected: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configured := tc.configured
			recommended := tc.recommended
			sanitizeCorruptConfiguredResources(&configured, &recommended, testClusterCPUCap, testClusterMemoryCap, tc.authoritative, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(tc.expected, configured); diff != "" {
				t.Fatalf("sanitizeCorruptConfiguredResources differs from expected, diff:\n%s", diff)
			}
		})
	}
}

func TestParseAuthoritativeDecreaseUsageBasis(t *testing.T) {
	testCases := []struct {
		name    string
		raw     string
		want    authoritativeDecreaseUsageBasis
		wantErr bool
	}{
		{name: "default empty", raw: "", want: authoritativeDecreaseUsageP80},
		{name: "p80", raw: "p80", want: authoritativeDecreaseUsageP80},
		{name: "peak", raw: "peak", want: authoritativeDecreaseUsagePeak},
		{name: "max alias", raw: "max", want: authoritativeDecreaseUsagePeak},
		{name: "invalid", raw: "invalid", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAuthoritativeDecreaseUsageBasis(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAuthoritativeDecreaseUsageBasis(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseAuthoritativeDecreaseUsageBasis(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestApplyAuthoritativeLimitDecrease_skipsBuildLimits(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(15e9, resource.BinarySI),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test-build-docker-build", WorkloadTypeBuild, false, "builds", authLegacyMemory(0.25), authoritativeDecreaseUsageP80, parseAuthoritativeSkipConfig("build", "", "", ""), nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("expected build limits unchanged with request decrease: %s", diff)
	}
}

func TestApplyAuthoritativeLimitDecrease_skipsBuildRequests(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(15e9, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test-build-docker-build", WorkloadTypeBuild, false, "builds", authLegacyMemory(0.25), authoritativeDecreaseUsageP80, parseAuthoritativeSkipConfig("", "", "build", ""), nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("expected build requests unchanged with limit decrease: %s", diff)
	}
}

func TestApplyAuthoritativeLimitDecrease_skipsBuildLimitsByClass(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(15e9, resource.BinarySI),
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test", WorkloadTypeProwjob, false, "builds", authLegacyMemory(0.25), authoritativeDecreaseUsageP80, parseAuthoritativeSkipConfig("", "builds", "", ""), nil, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("expected limits unchanged when workload class is skipped: %s", diff)
	}
}

func TestParseAuthoritativeSkipConfig(t *testing.T) {
	testCases := []struct {
		name           string
		limitTypes     string
		limitClasses   string
		requestTypes   string
		requestClasses string
		wantLimitTypes []string
		wantLimitClass []string
		wantReqTypes   []string
		wantReqClass   []string
	}{
		{
			name:           "parses trimmed comma-separated values",
			limitTypes:     " build,prowjob ",
			limitClasses:   "builds",
			requestTypes:   "build",
			requestClasses: " tests ",
			wantLimitTypes: []string{"build", "prowjob"},
			wantLimitClass: []string{"builds"},
			wantReqTypes:   []string{"build"},
			wantReqClass:   []string{"tests"},
		},
		{
			name:           "empty input yields empty sets",
			wantLimitTypes: nil,
			wantLimitClass: nil,
			wantReqTypes:   nil,
			wantReqClass:   nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAuthoritativeSkipConfig(tc.limitTypes, tc.limitClasses, tc.requestTypes, tc.requestClasses)
			assertSetEqual(t, "limit workload types", got.limitDecreaseWorkloadTypes, tc.wantLimitTypes)
			assertSetEqual(t, "limit workload classes", got.limitDecreaseWorkloadClasses, tc.wantLimitClass)
			assertSetEqual(t, "request workload types", got.requestDecreaseWorkloadTypes, tc.wantReqTypes)
			assertSetEqual(t, "request workload classes", got.requestDecreaseWorkloadClasses, tc.wantReqClass)
		})
	}
}

func assertSetEqual(t *testing.T, label string, got sets.Set[string], want []string) {
	t.Helper()
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("unexpected %s: %v", label, sets.List(got))
		}
		return
	}
	for _, item := range want {
		if !got.Has(item) {
			t.Fatalf("missing %s %q in %v", label, item, sets.List(got))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s count = %d, want %d (%v)", label, len(got), len(want), sets.List(got))
	}
}

func TestCappedIncreaseQuantity(t *testing.T) {
	testCases := []struct {
		name       string
		field      corev1.ResourceName
		configured string
		want       string
	}{
		{name: "memory cap", field: corev1.ResourceMemory, configured: "100Mi", want: "1000Mi"},
		{name: "memory cluster cap", field: corev1.ResourceMemory, configured: "5Gi", want: "20Gi"},
		{name: "cpu cap", field: corev1.ResourceCPU, configured: "500m", want: "5"},
		{name: "cpu cluster cap", field: corev1.ResourceCPU, configured: "2", want: "10"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configured := resource.MustParse(tc.configured)
			got := cappedIncreaseQuantity(tc.field, configured, testClusterCPUCap, testClusterMemoryCap)
			want := resource.MustParse(tc.want)
			if got.Cmp(want) != 0 {
				t.Fatalf("cappedIncreaseQuantity() = %s, want %s", got.String(), want.String())
			}
		})
	}
}

func TestIncreaseExceedsConfiguredThreshold(t *testing.T) {
	testCases := []struct {
		name       string
		field      corev1.ResourceName
		determined string
		configured string
		want       bool
	}{
		{name: "below threshold", field: corev1.ResourceMemory, determined: "200Mi", configured: "100Mi", want: false},
		{name: "above threshold", field: corev1.ResourceMemory, determined: "2Gi", configured: "100Mi", want: true},
		{name: "zero configured", field: corev1.ResourceMemory, determined: "1Gi", configured: "0", want: false},
		{name: "cpu uses millicores", field: corev1.ResourceCPU, determined: "2600m", configured: "250m", want: true},
		{name: "cpu below threshold in millicores", field: corev1.ResourceCPU, determined: "1500m", configured: "2", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			determined := resource.MustParse(tc.determined)
			configured := resource.MustParse(tc.configured)
			if got := increaseExceedsConfiguredThreshold(tc.field, determined, configured); got != tc.want {
				t.Fatalf("increaseExceedsConfiguredThreshold() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecommendationQuantityUsable(t *testing.T) {
	testCases := []struct {
		name  string
		field corev1.ResourceName
		value string
		want  bool
	}{
		{name: "cpu at minimum", field: corev1.ResourceCPU, value: "10m", want: true},
		{name: "cpu below minimum", field: corev1.ResourceCPU, value: "1m", want: false},
		{name: "memory at minimum", field: corev1.ResourceMemory, value: "1Mi", want: true},
		{name: "memory below minimum", field: corev1.ResourceMemory, value: "512Ki", want: false},
		{name: "memory corrupt idle stamp", field: corev1.ResourceMemory, value: "260Ki", want: false},
		{name: "zero memory", field: corev1.ResourceMemory, value: "0", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			q := resource.MustParse(tc.value)
			if got := recommendationQuantityUsable(tc.field, q); got != tc.want {
				t.Fatalf("recommendationQuantityUsable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplyAuthoritativeLimitDecrease_skipsDuringEscalation(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(1e1, resource.BinarySI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(2e10, resource.BinarySI),
		},
	}
	server := &escalationServer{
		index: podscaler.EscalationIndex{
			podscaler.WorkloadKey("build", "test"): {MemoryLevel: 1},
		},
	}

	applyAuthoritativeLimitDecrease(&ours, &theirs, "test", WorkloadTypeBuild, false, "", authLegacyMemory(0.25), authoritativeDecreaseUsageP80, authoritativeSkipConfig{}, server, logrus.WithField("test", t.Name()))
	want := *resource.NewQuantity(2e10, resource.BinarySI)
	if diff := cmp.Diff(theirs, corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{corev1.ResourceMemory: want},
		Requests: corev1.ResourceList{corev1.ResourceMemory: want},
	}); diff != "" {
		t.Errorf("expected memory unchanged during escalation: %s", diff)
	}
}

func TestApplyFailureEscalation(t *testing.T) {
	var testCases = []struct {
		name      string
		index     podscaler.EscalationIndex
		resources corev1.ResourceRequirements
	}{
		{
			name: "no escalation",
			resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		},
		{
			name: "cpu and memory escalation",
			index: podscaler.EscalationIndex{
				podscaler.WorkloadKey("build", "test"): {CPULevel: 1, MemoryLevel: 2},
			},
			resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := &escalationServer{index: tc.index, factor: 1.5}
			resources := copyResourceRequirements(tc.resources)
			expected := copyResourceRequirements(tc.resources)
			if tc.index != nil {
				state := tc.index[podscaler.WorkloadKey("build", "test")]
				if state.CPULevel > 0 {
					expected.Requests[corev1.ResourceCPU] = server.scaleQuantity(expected.Requests[corev1.ResourceCPU], state.CPULevel)
				}
				if state.MemoryLevel > 0 {
					expected.Requests[corev1.ResourceMemory] = server.scaleQuantity(expected.Requests[corev1.ResourceMemory], state.MemoryLevel)
					expected.Limits[corev1.ResourceMemory] = server.scaleQuantity(expected.Limits[corev1.ResourceMemory], state.MemoryLevel)
				}
			}
			applyFailureEscalation(&resources, "build", "test", server, logrus.WithField("test", tc.name))
			if diff := cmp.Diff(resources, expected); diff != "" {
				t.Errorf("unexpected resources: %s", diff)
			}
		})
	}
}

func copyResourceRequirements(in corev1.ResourceRequirements) corev1.ResourceRequirements {
	out := in
	if in.Requests != nil {
		out.Requests = in.Requests.DeepCopy()
	}
	if in.Limits != nil {
		out.Limits = in.Limits.DeepCopy()
	}
	return out
}

func TestUseOursIfLarger_authoritativeDryRun(t *testing.T) {
	ours := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
		},
	}
	theirs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
	}
	expected := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
		},
	}

	useOursIfLarger(&ours, &theirs, "test", "build", false, "", testClusterCPUCap, testClusterMemoryCap, &defaultReporter, logrus.WithField("test", t.Name()))
	if diff := cmp.Diff(theirs, expected); diff != "" {
		t.Errorf("dry-run should not mutate resources: %s", diff)
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
			theirs := copyResourceRequirements(tc.theirs)
			useOursIfLarger(&tc.ours, &theirs, "test", "build", false, "", testClusterCPUCap, testClusterMemoryCap, &tc.reporter, logrus.WithField("test", tc.name))

			if diff := cmp.Diff(tc.reporter.called, tc.expected); diff != "" {
				t.Errorf("actual and expected reporter states don't match, : %v", diff)
			}
			if tc.name == "ours is 10 times larger than theirs" {
				want := copyResourceRequirements(tc.theirs)
				want.Requests[corev1.ResourceMemory] = *resource.NewQuantity(1e1, resource.BinarySI)
				want.Limits[corev1.ResourceMemory] = *resource.NewQuantity(1e2, resource.BinarySI)
				if diff := cmp.Diff(theirs, want); diff != "" {
					t.Errorf("expected excessive increase to cap resources: %s", diff)
				}
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
			name: "leave CPU limits unchanged",
			input: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(200, resource.DecimalSI),
				},
			},
			expected: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(200, resource.DecimalSI),
				},
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
		{
			name: "too much memory limit",
			resources: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("25Gi"),
				},
			},
			expected: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse(memoryCap),
				},
			},
		},
		{
			name: "too much cpu limit",
			resources: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			expected: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(cpuCap, resource.DecimalSI),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preventUnschedulableWithCaps(tc.resources, *resource.NewQuantity(cpuCap, resource.DecimalSI), resource.MustParse(memoryCap), logrus.WithField("test", tc.name))
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
