package ephemeralcluster

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	aggerrs "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

type metric struct {
	Gauge     *gauge
	Histogram *histogram
}

type gauge struct {
	Labels []string
	Value  float64
}

type histogram struct {
	Labels      []string
	SampleCount uint64
	Buckets     []bucket
}

type bucket struct {
	UpperBound float64
	Count      uint64
}

func sortMetrics(a, b metric) int {
	metricLabels := func(m metric) []string {
		if m.Gauge != nil {
			sort.Strings(m.Gauge.Labels)
			return m.Gauge.Labels
		}
		if m.Histogram != nil {
			sort.Strings(m.Histogram.Labels)
			return m.Histogram.Labels
		}
		return nil
	}
	as := strings.Join(metricLabels(a), "")
	bs := strings.Join(metricLabels(b), "")
	return strings.Compare(as, bs)
}

type metricTransformer func(in *dto.Metric) metric

func gaugeTransformer(in *dto.Metric) metric {
	out := metric{Gauge: &gauge{Value: *in.Gauge.Value}}

	for _, l := range in.Label {
		out.Gauge.Labels = append(out.Gauge.Labels, *l.Value)
	}

	return out
}

func histogramTransformer(in *dto.Metric) metric {
	out := metric{
		Histogram: &histogram{
			SampleCount: in.Histogram.GetSampleCount(),
			Buckets:     make([]bucket, len(in.Histogram.Bucket)),
		},
	}

	var prevCumulativeCount uint64
	for i, b := range in.Histogram.Bucket {
		out.Histogram.Buckets[i] = bucket{
			UpperBound: b.GetUpperBound(),
			Count:      b.GetCumulativeCount() - prevCumulativeCount,
		}
		prevCumulativeCount = b.GetCumulativeCount()
	}

	out.Histogram.Buckets = append(out.Histogram.Buckets, bucket{
		UpperBound: math.Inf(1),
		Count:      in.Histogram.GetSampleCount() - prevCumulativeCount,
	})

	for _, l := range in.Label {
		out.Histogram.Labels = append(out.Histogram.Labels, *l.Value)
	}

	return out
}

func histogramBuckets(upperBounds []float64, counts ...uint64) []bucket {
	upperBounds = append(upperBounds, math.Inf(1))
	res := make([]bucket, len(upperBounds))
	for i, b := range upperBounds {
		res[i] = bucket{
			UpperBound: b,
		}
		if i < len(counts) {
			res[i].Count = counts[i]
		}
	}
	return res
}

func collect(v *prometheus.MetricVec, transform metricTransformer) ([]metric, error) {
	metricCh := make(chan prometheus.Metric)
	errs := make([]error, 0)
	var result []metric

	wg := sync.WaitGroup{}
	wg.Go(func() {
		for m := range metricCh {
			dtoMetric := dto.Metric{}
			if writeErr := m.Write(&dtoMetric); writeErr != nil {
				errs = append(errs, fmt.Errorf("write gauge: %w", writeErr))
				continue
			}
			result = append(result, transform(&dtoMetric))
		}
	})

	v.Collect(metricCh)
	close(metricCh)
	wg.Wait()

	return result, aggerrs.NewAggregate(errs)
}

func collectGauge(v *prometheus.MetricVec) (result []metric, err error) {
	return collect(v, gaugeTransformer)
}

func collectHistogram(v *prometheus.MetricVec) (result []metric, err error) {
	return collect(v, histogramTransformer)
}

func cmpMetrics(a, b []metric) string {
	slices.SortFunc(a, sortMetrics)
	slices.SortFunc(b, sortMetrics)
	return cmp.Diff(a, b)
}

func parseTime(t *testing.T, timeStr string) time.Time {
	parsed, err := time.Parse("2006-01-02 15:04:05", timeStr)
	if err != nil {
		t.Fatalf("parse fake now: %s", err)
	}
	return parsed
}

func fakeScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(ephemeralclusterv1.AddToScheme, prowv1.AddToScheme, corev1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatal("build scheme")
	}
	return scheme
}

func drainEvents(recorder *record.FakeRecorder) []string {
	close(recorder.Events)
	var events []string
	for e := range recorder.Events {
		events = append(events, e)
	}
	return events
}
