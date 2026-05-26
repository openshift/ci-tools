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
	"github.com/prometheus/common/model"
	jsonpatch "gopkg.in/evanphx/json-patch.v5"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestAdmissionAuthoritativeDryRun(t *testing.T) {
	t.Parallel()
	T := testhelper.NewT(interrupts.Context(), t)
	prometheusAddr, _ := prometheus.Initialize(T, T.TempDir(), rand.New(rand.NewSource(4641280330504625122)), false)

	kubeconfigFile := kubernetes.Fake(T, T.TempDir(), kubernetes.Prometheus(prometheusAddr), kubernetes.Builds(map[string]map[string]map[string]string{
		"namespace": {
			"withoutlabels": map[string]string{},
		},
	}))
	dataDir := T.TempDir()
	run.Producer(T, dataDir, kubeconfigFile, 0*time.Second)

	podLabels := map[string]string{
		"created-by-ci":                    "true",
		"ci.openshift.io/metadata.org":     "org",
		"ci.openshift.io/metadata.repo":    "repo",
		"ci.openshift.io/metadata.branch":  "branch",
		"ci.openshift.io/metadata.variant": "variant",
		"ci.openshift.io/metadata.target":  "target",
		"ci.openshift.io/metadata.step":    "step",
	}
	// Producer fixture histograms store counter-scale values; replace with low CPU rates so
	// admission can exercise authoritative decrease (see TestAdmission for unmodified increase path).
	seedLowCPURecommendationCache(T, dataDir, podscaler.MetadataFor(podLabels, "pod", "container"), 7)

	// Same Prometheus fixture as TestAdmission "pod for which we have data" (~10 CPU recommendation).
	const configuredMilli = 100_000                     // 100 CPU
	wantMilli := int64(float64(configuredMilli) * 0.75) // 25% authoritative cap
	cpuCap := int64(200)

	highCPU := *resource.NewMilliQuantity(configuredMilli, resource.DecimalSI)
	wantCPU := *resource.NewMilliQuantity(wantMilli, resource.DecimalSI)
	basePod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pod",
			Labels: podLabels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "container",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: highCPU,
					},
				},
			}},
		},
	}

	t.Run("dry-run does not decrease CPU", func(t *testing.T) {
		ctx, cancel := context.WithCancel(interrupts.Context())
		defer cancel()
		admissionHost, transport := run.Admission(T, dataDir, kubeconfigFile, ctx, false,
			"--authoritative-cpu=true",
			"--authoritative-cpu-dry-run=true",
			fmt.Sprintf("--cpu-cap=%d", cpuCap),
		)
		got := cpuRequestAfterAdmission(t, &basePod, admissionHost, transport)
		if got.Cmp(highCPU) != 0 {
			t.Fatalf("dry-run changed CPU request from %s to %s", highCPU.String(), got.String())
		}
	})

	t.Run("authoritative decreases CPU", func(t *testing.T) {
		ctx, cancel := context.WithCancel(interrupts.Context())
		defer cancel()
		admissionHost, transport := run.Admission(T, dataDir, kubeconfigFile, ctx, false, "--authoritative-cpu=true", fmt.Sprintf("--cpu-cap=%d", cpuCap))
		got := cpuRequestAfterAdmission(t, &basePod, admissionHost, transport)
		if got.Cmp(wantCPU) != 0 {
			t.Fatalf("authoritative CPU request = %s, want %s", got.String(), wantCPU.String())
		}
	})
}

func cpuRequestAfterAdmission(t *testing.T, pod *corev1.Pod, admissionHost string, transport *http.Transport) resource.Quantity {
	t.Helper()
	pod = pod.DeepCopy()
	pod.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}
	rawPod, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	request := admissionv1.AdmissionRequest{
		UID:      "705ab4f5-6393-11e8-b7cc-42010a800003",
		Kind:     metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
		Resource: metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
		Object:   runtime.RawExtension{Raw: rawPod},
	}
	rawReview, err := json.Marshal(admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request:  &request,
	})
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	client := http.Client{Transport: transport}
	response, err := client.Post(admissionHost+"/pods", "application/json", bytes.NewBuffer(rawReview))
	if err != nil {
		t.Fatalf("post admission: %v", err)
	}
	defer response.Body.Close()
	rawResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(rawResponse, &review); err != nil {
		t.Fatalf("unmarshal review: %v", err)
	}
	if review.Response == nil || !review.Response.Allowed {
		t.Fatalf("admission not allowed: %+v", review.Response)
	}
	patched, err := applyJSONPatchToPod(pod, review.Response.Patch)
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	cpu := patched.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	if cpu.IsZero() {
		t.Fatal("no CPU request on patched pod")
	}
	return cpu
}

func applyJSONPatchToPod(pod *corev1.Pod, patch []byte) (*corev1.Pod, error) {
	raw, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	if len(patch) == 0 {
		out := pod.DeepCopy()
		return out, nil
	}
	decoded, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		return nil, err
	}
	patched, err := decoded.Apply(raw)
	if err != nil {
		return nil, err
	}
	var out corev1.Pod
	if err := json.Unmarshal(patched, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func seedLowCPURecommendationCache(t testhelper.TestingTInterface, dataDir string, meta podscaler.FullMetadata, coresAtQuantile float64) {
	t.Helper()
	hist := circonusllhist.New(circonusllhist.NoLookup())
	for i := 0; i < 500; i++ {
		if err := hist.RecordValue(coresAtQuantile); err != nil {
			t.Fatalf("record histogram value: %v", err)
		}
	}
	wrapped := circonusllhist.NewHistogramWithoutLookups(hist)
	fp := model.Fingerprint(0xcafe)
	for _, prefix := range []string{"steps", "pods", "prowjobs"} {
		path := filepath.Join(dataDir, prefix, "container_cpu_usage_seconds_total.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s CPU cache: %v", prefix, err)
		}
		var cache podscaler.CachedQuery
		if err := json.Unmarshal(raw, &cache); err != nil {
			t.Fatalf("unmarshal %s CPU cache: %v", prefix, err)
		}
		cache.Data = map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{fp: wrapped}
		cache.DataByMetaData = map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			meta: {{Fingerprint: fp, Added: time.Now()}},
		}
		encoded, err := json.Marshal(&cache)
		if err != nil {
			t.Fatalf("marshal %s CPU cache: %v", prefix, err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write %s CPU cache: %v", prefix, err)
		}
	}
}
