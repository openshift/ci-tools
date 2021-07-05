// +build e2e

package pod_scaler

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"

	"k8s.io/test-infra/prow/interrupts"

	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/kubernetes"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/prometheus"
	"github.com/openshift/ci-tools/test/e2e/pod-scaler/run"
)

func TestProduce(t *testing.T) {
	t.Parallel()
	T := testhelper.NewT(interrupts.Context(), t)
	prometheusAddr, info := prometheus.Initialize(T, T.TempDir())
	kubeconfigFile := kubernetes.Fake(T, T.TempDir(), prometheusAddr)

	dataDir := T.TempDir()
	// we need to run the data collection in order of largest offsets as smaller ones subsume the data
	var offsets []time.Duration
	for offset := range info.ByOffset {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] > offsets[j]
	})
	expected := prometheus.Data{ByFile: map[string]map[pod_scaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{}}
	for _, offset := range offsets {
		// we expect the data from this offset and all earlier ones, too
		data := info.ByOffset[offset]
		for filename := range data.ByFile {
			if _, ok := expected.ByFile[filename]; !ok {
				expected.ByFile[filename] = map[pod_scaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{}
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
		var c pod_scaler.CachedQuery
		raw, err := ioutil.ReadFile(filepath.Join(dataDir, filename))
		if err != nil {
			t.Fatalf("%s: failed to read cache: %v", filename, err)
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("%s: failed to unmarshal cache: %v", filename, err)
		}
		actualIdentifiers, expectedIdentifiers := map[pod_scaler.FullMetadata]interface{}{}, map[pod_scaler.FullMetadata]interface{}{}
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
			for _, fingerprint := range c.DataByMetaData[identifier] {
				hist := c.Data[fingerprint].Histogram()
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
				for _, fingerprint := range c.DataByMetaData[identifier] {
					if hist.Equals(c.Data[fingerprint].Histogram()) {
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
