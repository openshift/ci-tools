package httphelper

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TraceResponseWriter provides a wrapper to write data and header
type TraceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

// WriteHeader writes the header to the response
func (w *TraceResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Write writes the body of the response
func (w *TraceResponseWriter) Write(data []byte) (int, error) {
	size, err := w.ResponseWriter.Write(data)
	w.size += size
	return size, err
}

// Metrics is responsible for holding the metrics for Prometheus
type Metrics struct {
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPResponseSize    *prometheus.HistogramVec
	ErrorRate           *prometheus.CounterVec
}

// NewMetrics is a constructor for Metrics
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{

		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "http_request_duration_seconds",
				Help:      "http request duration in seconds",
				Buckets:   []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
			},
			[]string{"status", "path"},
		),
		HTTPResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "http_response_size_bytes",
				Help:      "http response size in bytes",
				Buckets:   []float64{256, 512, 1024, 2048, 4096, 6144, 8192, 10240, 12288, 16384, 24576, 32768, 40960, 49152, 57344, 65536},
			},
			[]string{"status", "path"},
		),
		ErrorRate: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "error_rate",
				Help:      "number of errors, sorted by label/type",
			},
			[]string{"error"},
		),
	}
	prometheus.MustRegister(m.HTTPRequestDuration)
	prometheus.MustRegister(m.HTTPResponseSize)
	prometheus.MustRegister(m.ErrorRate)
	return m
}

// RecordError is responsible for recording the error to prometheus
func (m *Metrics) RecordError(label string) {
	labels := prometheus.Labels{"error": label}
	if m.ErrorRate != nil {
		m.ErrorRate.With(labels).Inc()
	}
}

// HandleWithMetricsCustomTimer allows the for a custom timer to be used
// It is an abstraction to allow testing of HandleWithMetrics
func (m *Metrics) HandleWithMetricsCustomTimer(h http.HandlerFunc, timeSince func(time.Time) time.Duration) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m != nil {
			t := time.Now()
			// Initialize the status to 200 in case WriteHeader is not called
			trw := &TraceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			h(trw, r)
			latency := timeSince(t)
			labels := prometheus.Labels{"status": strconv.Itoa(trw.statusCode), "path": r.URL.EscapedPath()}
			if m.HTTPRequestDuration != nil {
				m.HTTPRequestDuration.With(labels).Observe(latency.Seconds())
			}
			if m.HTTPResponseSize != nil {
				m.HTTPResponseSize.With(labels).Observe(float64(trw.size))
			}
		}

	})
}

// HandleWithMetrics is a wrapper to log metrics for Handler functions
func (m *Metrics) HandleWithMetrics(h http.HandlerFunc) http.HandlerFunc {
	return m.HandleWithMetricsCustomTimer(h, time.Since)
}
