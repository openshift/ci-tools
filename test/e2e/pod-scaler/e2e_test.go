//go:build e2e
// +build e2e

package pod_scaler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/prow/pkg/interrupts"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/kubernetes"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/prometheus"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/run"
)

func TestProduce(t *testing.T) {
	t.Parallel()
	T := testhelper.NewT(interrupts.Context(), t)
	prometheusAddr, info := prometheus.Initialize(T, T.TempDir(), rand.New(rand.NewSource(4641280330504625122)), false)
	kubeconfigFile := kubernetes.Fake(T, T.TempDir(), kubernetes.Prometheus(prometheusAddr))

	dataDir := T.TempDir()
	// we need to run the data collection in order of largest offsets as smaller ones subsume the data
	var offsets []time.Duration
	for offset := range info.ByOffset {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] > offsets[j]
	})
	expected := prometheus.Data{ByFile: map[string]map[podscaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{}}
	for _, offset := range offsets {
		// we expect the data from this offset and all earlier ones, too
		data := info.ByOffset[offset]
		for filename := range data.ByFile {
			if _, ok := expected.ByFile[filename]; !ok {
				expected.ByFile[filename] = map[podscaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{}
			}
			for identifier, hists := range data.ByFile[filename] {
				expected.ByFile[filename][identifier] = append(expected.ByFile[filename][identifier], hists...)
			}
		}
		run.Producer(T, dataDir, kubeconfigFile, offset)
		check(t, dataDir, expected)
	}
}

// we can't simply check static output files to determine that the producer worked as expected, for a number of reasons:
// - the data structure we use to store the time ranges we've queried holds time stamps that are dynamically determined
// - we store Prometheus data by series fingerprint, which we can't determine ahead of time
func check(t *testing.T, dataDir string, checkAgainst prometheus.Data) {
	for filename, data := range checkAgainst.ByFile {
		var c podscaler.CachedQuery
		raw, err := os.ReadFile(filepath.Join(dataDir, filename))
		if err != nil {
			t.Fatalf("%s: failed to read cache: %v", filename, err)
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("%s: failed to unmarshal cache: %v", filename, err)
		}
		actualIdentifiers, expectedIdentifiers := map[podscaler.FullMetadata]interface{}{}, map[podscaler.FullMetadata]interface{}{}
		for item := range data {
			expectedIdentifiers[item] = nil
		}
		for item := range c.DataByMetaData {
			actualIdentifiers[item] = nil
		}
		if diff := cmp.Diff(actualIdentifiers, expectedIdentifiers); diff != "" {
			t.Errorf("%s: did not get correct identifiers: %v", filename, diff)
		}
		for identifier := range c.DataByMetaData {
			if actual, expected := len(c.DataByMetaData[identifier]), len(data[identifier]); actual != expected {
				t.Errorf("%s: %s: expected %d histograms but got %d", filename, identifier.String(), expected, actual)
			}
			if strings.Contains(filename, "container_cpu_usage_seconds_total") {
				// no sensible way to assert about histograms containing data using rate()
				continue
			}
			for _, fingerprintTime := range c.DataByMetaData[identifier] {
				hist := c.Data[fingerprintTime.Fingerprint].Histogram()
				var found bool
				for _, other := range data[identifier] {
					if hist.Equals(other.Histogram()) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: %s: did not expect %d samples: %v", filename, identifier.String(), hist.Count(), hist)
				}
			}
			for _, item := range data[identifier] {
				hist := item.Histogram()
				var found bool
				for _, fingerprintTime := range c.DataByMetaData[identifier] {
					if hist.Equals(c.Data[fingerprintTime.Fingerprint].Histogram()) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: %s: did not find %d samples: %v", filename, identifier.String(), hist.Count(), hist)
				}
			}
		}
	}
}

func TestBuildPodAdmission(t *testing.T) {
	t.Parallel()
	T := testhelper.NewT(interrupts.Context(), t)
	kubeconfigFile := kubernetes.Fake(T, T.TempDir(), kubernetes.Builds(map[string]map[string]map[string]string{
		"namespace": {
			"withoutlabels": map[string]string{},
			"withlabels": map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			},
		},
	}))
	ctx, cancel := context.WithCancel(interrupts.Context())
	defer func() {
		cancel()
	}()
	dataDir := T.TempDir()
	for _, set := range []string{"pods", "prowjobs", "steps"} {
		for _, metric := range []string{"container_memory_working_set_bytes", "container_cpu_usage_seconds_total"} {
			if err := os.MkdirAll(filepath.Join(dataDir, set), 0777); err != nil {
				t.Fatalf("could not seed data dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, set, metric+".json"), []byte(`{}`), 0777); err != nil {
				t.Fatalf("could not seed data dir: %v", err)
			}
		}
	}

	admissionHost, transport := run.Admission(T, dataDir, kubeconfigFile, ctx, false)
	admissionClient := http.Client{Transport: transport}

	var testCases = []struct {
		name    string
		request admissionv1.AdmissionRequest
	}{
		{
			name: "not a pod",
			request: admissionv1.AdmissionRequest{
				UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
				Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
				Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
				Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Secret","metadata": {"name": "somethingelse","namespace": "namespace"}}`)},
			},
		},
		{
			name: "pod not associated with a build",
			request: admissionv1.AdmissionRequest{
				UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
				Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
				Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
				Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "name": "somethingelse","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
			},
		},
		{
			name: "pod associated with a build that has no labels",
			request: admissionv1.AdmissionRequest{
				UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
				Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
				Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
				Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withoutlabels"}, "annotations": {"openshift.io/build.name": "withoutlabels"}, "name": "withoutlabels-build","namespace": "namespace"}, "spec":{"containers":[]}, "status":{}}`)},
			},
		},
		{
			name: "pod associated with a build with labels",
			request: admissionv1.AdmissionRequest{
				UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
				Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
				Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
				Object:   runtime.RawExtension{Raw: []byte(`{"apiVersion": "v1","kind": "Pod","metadata": {"creationTimestamp": null, "labels": {"openshift.io/build.name": "withlabels"}, "annotations": {"openshift.io/build.name": "withlabels"}, "name": "withlabels-build","namespace": "namespace"}, "spec":{"containers":[{"name":"test"},{"name":"other"}]}, "status":{}}`)},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw, err := json.Marshal(admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					Kind:       "AdmissionReview",
					APIVersion: "admission.k8s.io/v1",
				},
				Request: &testCase.request,
			})
			if err != nil {
				t.Fatalf("could not marshal request: %v", err)
			}
			response, err := admissionClient.Post(fmt.Sprintf("%s/pods", admissionHost), "application/json", bytes.NewBuffer(raw))
			if err != nil {
				t.Fatalf("could not post request: %v", err)
			}
			rawResponse, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("could not read response: %v", err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("could not close response: %v", err)
			}
			testhelper.CompareWithFixture(t, rawResponse)

			var review admissionv1.AdmissionReview
			if err := json.Unmarshal(rawResponse, &review); err != nil { // TODO: use scheme?
				t.Fatalf("could not unmarshal response: %v", err)
			}
			testhelper.CompareWithFixture(t, review.Response.Patch, testhelper.WithSuffix("-patch"))
		})
	}
}

func TestAdmission(t *testing.T) {
	t.Parallel()
	T := testhelper.NewT(interrupts.Context(), t)
	prometheusAddr, _ := prometheus.Initialize(T, T.TempDir(), rand.New(rand.NewSource(4641280330504625122)), false)

	kubeconfigFile := kubernetes.Fake(T, T.TempDir(), kubernetes.Prometheus(prometheusAddr), kubernetes.Builds(map[string]map[string]map[string]string{
		"namespace": {
			"withoutlabels": map[string]string{},
			"withlabels": map[string]string{
				"ci.openshift.io/metadata.org":     "org",
				"ci.openshift.io/metadata.repo":    "repo",
				"ci.openshift.io/metadata.branch":  "branch",
				"ci.openshift.io/metadata.variant": "variant",
				"ci.openshift.io/metadata.target":  "target",
			},
		},
	}))
	ctx, cancel := context.WithCancel(interrupts.Context())
	defer func() {
		cancel()
	}()
	dataDir := T.TempDir()
	run.Producer(T, dataDir, kubeconfigFile, 0*time.Second)
	admissionHost, transport := run.Admission(T, dataDir, kubeconfigFile, ctx, false)
	admissionClient := http.Client{Transport: transport}

	var testCases = []struct {
		name string
		pod  corev1.Pod
	}{
		{
			name: "pod for which we have no data",
			pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "pod",
				Labels: map[string]string{
					"created-by-ci":                    "true",
					"ci.openshift.io/metadata.org":     "org",
					"ci.openshift.io/metadata.repo":    "repo",
					"ci.openshift.io/metadata.branch":  "branch",
					"ci.openshift.io/metadata.variant": "variant",
					"ci.openshift.io/metadata.target":  "NO-PREVIOUS-DATA",
				}}},
		},
		{
			name: "pod for which we have data",
			pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: "pod",
				Labels: map[string]string{
					"created-by-ci":                    "true",
					"ci.openshift.io/metadata.org":     "org",
					"ci.openshift.io/metadata.repo":    "repo",
					"ci.openshift.io/metadata.branch":  "branch",
					"ci.openshift.io/metadata.variant": "variant",
					"ci.openshift.io/metadata.target":  "target",
					"ci.openshift.io/metadata.step":    "step",
				}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "container"}},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.pod.TypeMeta = metav1.TypeMeta{
				Kind:       "Pod",
				APIVersion: "v1",
			}
			rawPod, err := json.Marshal(&testCase.pod)
			if err != nil {
				t.Fatalf("failed to marshal pod: %v", err)
			}

			request := admissionv1.AdmissionRequest{
				UID:      "705ab4f5-6393-11e8-b7cc-42010a800002",
				Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
				Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
				Object:   runtime.RawExtension{Raw: rawPod},
			}
			rawReview, err := json.Marshal(admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					Kind:       "AdmissionReview",
					APIVersion: "admission.k8s.io/v1",
				},
				Request: &request,
			})
			if err != nil {
				t.Fatalf("could not marshal request: %v", err)
			}
			response, err := admissionClient.Post(fmt.Sprintf("%s/pods", admissionHost), "application/json", bytes.NewBuffer(rawReview))
			if err != nil {
				t.Fatalf("could not post request: %v", err)
			}
			rawResponse, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("could not read response: %v", err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("could not close response: %v", err)
			}
			testhelper.CompareWithFixture(t, rawResponse)

			var review admissionv1.AdmissionReview
			if err := json.Unmarshal(rawResponse, &review); err != nil {
				t.Fatalf("could not unmarshal response: %v", err)
			}
			testhelper.CompareWithFixture(t, review.Response.Patch, testhelper.WithSuffix("-patch"))
		})
	}
}
