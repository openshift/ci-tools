//go:build e2e
// +build e2e

package prometheus

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/openhistogram/circonusllhist"
	prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/pkg/value"
	uuid "github.com/satori/go.uuid"

	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

const (
	// numSeries is the number of individual series of data generated for a given label set
	numSeries = 5
	// numSamples is the number of samples in an individual series
	numSamples = 250
	// samplingDuration is the duration between samples in a series
	samplingDuration = time.Minute
	// seriesDuration
	seriesDuration = time.Duration(numSamples) * samplingDuration
)

type DataInStages struct {
	ByOffset map[time.Duration]Data
}

type Data struct {
	ByFile map[string]map[pod_scaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups
}

func (d *DataInStages) record(offset time.Duration, metric string, labels seriesLabels, identifier pod_scaler.FullMetadata, data []float64) error {
	hist := circonusllhist.New(circonusllhist.NoLookup(), circonusllhist.Size(1))
	for _, v := range data {
		if err := hist.RecordValue(v); err != nil {
			return fmt.Errorf("could not insert value into histogram: %w", err)
		}
	}

	if _, ok := d.ByOffset[offset]; !ok {
		d.ByOffset[offset] = Data{
			ByFile: map[string]map[pod_scaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{},
		}
	}
	var file string
	switch {
	case labels["label_ci_openshift_io_metadata_step"] != "":
		file = fmt.Sprintf("steps/%s.json", metric)
	case labels["label_created_by_prow"] != "":
		file = fmt.Sprintf("prowjobs/%s.json", metric)
	default:
		file = fmt.Sprintf("pods/%s.json", metric)
	}
	if _, ok := d.ByOffset[offset].ByFile[file]; !ok {
		d.ByOffset[offset].ByFile[file] = map[pod_scaler.FullMetadata][]*circonusllhist.HistogramWithoutLookups{}
	}
	d.ByOffset[offset].ByFile[file][identifier] = append(d.ByOffset[offset].ByFile[file][identifier], circonusllhist.NewHistogramWithoutLookups(hist))

	return nil
}

// Backfill generates data and backfills it into Prometheus, returning the data written by stage.
func Backfill(t testhelper.TestingTInterface, prometheusDir string, retentionPeriod time.Duration, r *rand.Rand) *DataInStages {
	t.Log("Backfilling Prometheus data.")
	generateStart := time.Now()
	info, families := generateData(t, retentionPeriod, r)
	t.Logf("Generated Prometheus data in %s.", time.Since(generateStart))

	writeStart := time.Now()
	prometheusBackfillFile, err := os.CreateTemp(prometheusDir, "backfill")
	if err != nil {
		t.Fatalf("Could not create temporary file for Prometheus backfill: %v", err)
	}
	encoder := expfmt.NewEncoder(prometheusBackfillFile, expfmt.FmtOpenMetrics_0_0_1)
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatalf("Failed to write Prometheus backfill data: %v", err)
		}
	}
	closer := encoder.(expfmt.Closer)
	if err := closer.Close(); err != nil {
		t.Fatalf("Failed to close Prometheus backfill data: %v", err)
	}
	if err := prometheusBackfillFile.Close(); err != nil {
		t.Fatalf("Failed to close temporary Prometheus backfill file: %v", err)
	}
	t.Logf("Wrote generated Prometheus data in %s.", time.Since(writeStart))

	backfillStart := time.Now()
	backfillArgs := []string{
		"promtool", "tsdb", "create-blocks-from", "openmetrics",
		prometheusBackfillFile.Name(),
		prometheusDir,
		"--quiet",
		"--max-block-duration=10000h",
	}
	t.Logf("Running backfill: %v", strings.Join(backfillArgs, " "))
	backfill := exec.Command(backfillArgs[0], backfillArgs[1:]...)
	if out, err := backfill.CombinedOutput(); err != nil {
		t.Fatalf("Failed to backfill Prometheus: %v: %v", err, string(out))
	}

	t.Logf("Backfilled Prometheus data in %s.", time.Since(backfillStart))
	return info
}

func generateData(t testhelper.TestingTInterface, retentionPeriod time.Duration, r *rand.Rand) (*DataInStages, []*prometheus_client.MetricFamily) {
	info := &DataInStages{ByOffset: map[time.Duration]Data{}}
	metricTypePtr := func(metric prometheus_client.MetricType) *prometheus_client.MetricType { return &metric }
	kubePodLabels := prometheus_client.MetricFamily{
		Name:   pointer.StringPtr("kube_pod_labels"),
		Type:   metricTypePtr(prometheus_client.MetricType_COUNTER),
		Metric: []*prometheus_client.Metric{},
	}
	families := []*prometheus_client.MetricFamily{&kubePodLabels}
	for _, metric := range metricGenerators() {
		family := prometheus_client.MetricFamily{
			Name:   pointer.StringPtr(metric.metricName),
			Type:   metricTypePtr(metric.metricType),
			Metric: []*prometheus_client.Metric{},
		}
		for _, item := range series() {
			mean := float64(r.Int31()) / 10
			stddev := r.Float64() * mean / 20.0
			for _, offset := range offsets(retentionPeriod) {
				for j := 0; j < numSeries; j++ {
					labelPairs := labelsFor(item.labels)
					var values []float64
					start := time.Now().Add(-time.Duration(r.Float64()*float64(retentionPeriod/2-seriesDuration)) - offset - seriesDuration).Round(1 * time.Minute)
					for k := 0; k < numSamples; k++ {
						ts := pointer.Int64Ptr(start.Add(time.Duration(k)*samplingDuration).UnixNano() / 1e6)

						// record a new value in the metric we're generating
						m := &prometheus_client.Metric{
							Label:       labelPairs,
							TimestampMs: ts,
						}
						metric.addValue(m, r.NormFloat64()*stddev+mean, float64(k))
						family.Metric = append(family.Metric, m)

						values = append(values, metric.getValue(m))

						// record a value in the kube_pod_labels metric, which we join on during queries
						kubePodLabels.Metric = append(kubePodLabels.Metric, &prometheus_client.Metric{
							Label:       labelPairs,
							TimestampMs: ts,
							Counter:     &prometheus_client.Counter{Value: pointer.Float64Ptr(1)},
						})
					}

					// add a stale marker to the end of the series to ensure that Prometheus knows this data is done
					stale := &prometheus_client.Metric{
						Label:       labelPairs,
						TimestampMs: pointer.Int64Ptr(start.Add(time.Duration(numSamples)*samplingDuration).UnixNano() / 1e6),
					}
					metric.addValue(stale, math.Float64frombits(value.StaleNaN), 1)
					family.Metric = append(family.Metric, stale)

					if err := info.record(offset, metric.metricName, item.labels, item.meta, values); err != nil {
						t.Fatalf("could not record values into hist: %v", err)
					}
				}
			}
		}
		families = append(families, &family)
	}
	return info, families
}

// metricGenerator describes a metric and closes over value operations on the Metric type
type metricGenerator struct {
	metricName string
	metricType prometheus_client.MetricType
	addValue   func(*prometheus_client.Metric, float64, float64)
	getValue   func(*prometheus_client.Metric) float64
}

func metricGenerators() []metricGenerator {
	return []metricGenerator{
		{
			metricName: "container_cpu_usage_seconds_total",
			metricType: prometheus_client.MetricType_COUNTER,
			addValue: func(metric *prometheus_client.Metric, f, j float64) {
				metric.Counter = &prometheus_client.Counter{Value: pointer.Float64Ptr(f*j + 1)} // we're interested in measuring the rate
			},
			getValue: func(metric *prometheus_client.Metric) float64 {
				return *metric.Counter.Value
			},
		},
		{
			metricName: "container_memory_working_set_bytes",
			metricType: prometheus_client.MetricType_GAUGE,
			addValue: func(metric *prometheus_client.Metric, f, _ float64) {
				metric.Gauge = &prometheus_client.Gauge{Value: pointer.Float64Ptr(f)}
			},
			getValue: func(metric *prometheus_client.Metric) float64 {
				return *metric.Gauge.Value
			},
		},
	}
}

// seriesLabels holds a Prometheus label set for a series of data
type seriesLabels map[string]string

// seriesInfo connects a set of labels with the metadata it maps to
type seriesInfo struct {
	meta   pod_scaler.FullMetadata
	labels seriesLabels
}

func series() []seriesInfo {
	return []seriesInfo{
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Target:    "target",
				Step:      "step",
				Pod:       "pod",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_ci":                    "true",
				"label_ci_openshift_io_metadata_org":     "org",
				"label_ci_openshift_io_metadata_repo":    "repo",
				"label_ci_openshift_io_metadata_branch":  "branch",
				"label_ci_openshift_io_metadata_variant": "variant",
				"label_ci_openshift_io_metadata_target":  "target",
				"label_ci_openshift_io_metadata_step":    "step",
				"pod":                                    "pod",
				"container":                              "container",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "src-build",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_ci":                    "true",
				"label_ci_openshift_io_metadata_org":     "org",
				"label_ci_openshift_io_metadata_repo":    "repo",
				"label_ci_openshift_io_metadata_branch":  "branch",
				"label_ci_openshift_io_metadata_variant": "variant",
				"label_ci_openshift_io_metadata_target":  "target",
				"label_openshift_io_build_name":          "src",
				"pod":                                    "src-build",
				"container":                              "container",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Pod:       "release-latest-cli",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_ci":                    "true",
				"label_ci_openshift_io_metadata_org":     "org",
				"label_ci_openshift_io_metadata_repo":    "repo",
				"label_ci_openshift_io_metadata_branch":  "branch",
				"label_ci_openshift_io_metadata_variant": "variant",
				"label_ci_openshift_io_metadata_target":  "target",
				"label_ci_openshift_io_release":          "latest",
				"pod":                                    "release-latest-cli",
				"container":                              "container",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				},
				Container: "rpm-repo",
			},
			labels: seriesLabels{
				"label_created_by_ci":                    "true",
				"label_ci_openshift_io_metadata_org":     "org",
				"label_ci_openshift_io_metadata_repo":    "repo",
				"label_ci_openshift_io_metadata_branch":  "branch",
				"label_ci_openshift_io_metadata_variant": "variant",
				"label_ci_openshift_io_metadata_target":  "target",
				"label_app":                              "rpm-repo",
				"pod":                                    "rpm-repo-5d88d4fc4c-jg2xb",
				"container":                              "rpm-repo",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_prow":           "true",
				"label_prow_k8s_io_refs_org":      "org",
				"label_prow_k8s_io_refs_repo":     "repo",
				"label_prow_k8s_io_refs_base_ref": "branch",
				"label_prow_k8s_io_context":       "context",
				"label_prow_k8s_io_job":           "pull-ci-org-repo-branch-context",
				"pod":                             "d316d4cc-a438-11eb-b35f-0a580a800e92",
				"container":                       "container",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				},
				Target:    "context",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_prow":           "true",
				"label_prow_k8s_io_refs_org":      "org",
				"label_prow_k8s_io_refs_repo":     "repo",
				"label_prow_k8s_io_refs_base_ref": "branch",
				"label_prow_k8s_io_context":       "",
				"label_prow_k8s_io_job":           "periodic-ci-org-repo-branch-context",
				"label_prow_k8s_io_type":          "periodic",
				"pod":                             "d316d4cc-a437-11eb-b35f-0a580a800e92",
				"container":                       "container",
			},
		},
		{
			meta: pod_scaler.FullMetadata{
				Target:    "periodic-handwritten-prowjob",
				Container: "container",
			},
			labels: seriesLabels{
				"label_created_by_prow":     "true",
				"label_prow_k8s_io_context": "",
				"label_prow_k8s_io_job":     "periodic-handwritten-prowjob",
				"label_prow_k8s_io_type":    "periodic",
				"pod":                       "d316d4cc-a437-11eb-b35f-0a580a800e92",
				"container":                 "container",
			},
		},
	}
}

// offsets determines the offsets we generate data around given a retention period. We want to generate data in every
// interval so that in the future we can assert that incremental queries work to add new data to the dataset.
func offsets(retentionPeriod time.Duration) []time.Duration {
	return []time.Duration{0, retentionPeriod / 2}
}

// labelsFor turns a raw map of labels into a set of label pairs, adding random data for required but missing labels
func labelsFor(raw map[string]string) []*prometheus_client.LabelPair {
	localSet := map[string]string{}
	for k, v := range raw {
		localSet[k] = v
	}
	for _, required := range []string{"namespace", "pod", "container"} {
		if _, set := localSet[required]; set {
			continue
		}
		localSet[required] = uuid.NewV4().String()
	}
	var labels []*prometheus_client.LabelPair
	for k, v := range localSet {
		labels = append(labels, &prometheus_client.LabelPair{
			Name:  pointer.StringPtr(k),
			Value: pointer.StringPtr(v),
		})
	}
	return labels
}
