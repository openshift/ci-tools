package main

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	buildv1 "github.com/openshift/api/build/v1"
	fakebuildv1client "github.com/openshift/client-go/build/clientset/versioned/fake"

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
					"ci.openshift.io/metadata.step":    "step",
				},
			},
		},
	)
	decoder, err := admission.NewDecoder(scheme.Scheme)
	if err != nil {
		t.Fatalf("failed to create decoder from scheme: %v", err)
	}
	mutator := podMutator{
		logger:  logrus.WithField("test", t.Name()),
		client:  client.BuildV1(),
		decoder: decoder,
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
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withoutlabels"}, "name": "withoutlabels-build","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
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
					Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withlabels"}, "name": "withoutlabels-build","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
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

func TestMutatePod(t *testing.T) {
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
				"ci.openshift.io/metadata.step":    "step",
			}}},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"ci.openshift.io/metadata.step":    "step",
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
				"ci.openshift.io/metadata.step":    "step",
			}}},
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":  "org",
				"ci.openshift.io/metadata.repo": "repo",
				"ci.openshift.io/metadata.step": "step",
			}}},
			expected: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
				"ci.openshift.io/metadata.step":    "step",
			}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mutatePod(testCase.pod, testCase.build)
			if diff := cmp.Diff(testCase.pod, testCase.expected); diff != "" {
				t.Errorf("%s: got incorrect pod after mutation: %v", testCase.name, diff)
			}
		})
	}
}
