package httphelper

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type TraceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (w *TraceResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *TraceResponseWriter) Write(data []byte) (int, error) {
	size, err := w.ResponseWriter.Write(data)
	w.size += size
	return size, err
}

type Metrics struct {
	HttpRequestDuration *prometheus.HistogramVec
	HttpResponseSize    *prometheus.HistogramVec
	ErrorRate           *prometheus.CounterVec
}

func RecordError(label string, metrics *Metrics) {
	labels := prometheus.Labels{"error": label}
	metrics.ErrorRate.With(labels).Inc()
}

func HandleWithMetrics(h http.HandlerFunc, metrics *Metrics) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if metrics != nil {
			t := time.Now()
			// Initialize the status to 200 in case WriteHeader is not called
			trw := &TraceResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			h(trw, r)
			latency := time.Since(t)
			labels := prometheus.Labels{"status": strconv.Itoa(trw.statusCode), "path": r.URL.EscapedPath()}
			if metrics.HttpRequestDuration != nil {
				metrics.HttpRequestDuration.With(labels).Observe(latency.Seconds())
			}
			if metrics.HttpResponseSize != nil {
				metrics.HttpResponseSize.With(labels).Observe(float64(trw.size))
			}
		}

	})
}
