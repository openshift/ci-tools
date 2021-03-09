package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/prometheus/client_golang/prometheus"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
)

type statusCodeCapturingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
}

func (l *statusCodeCapturingResponseWriter) Write(p []byte) (n int, err error) {
	l.wroteHeader = true
	return l.ResponseWriter.Write(p)
}

func (l *statusCodeCapturingResponseWriter) WriteHeader(code int) {
	if !l.wroteHeader {
		l.statusCode = code
		l.wroteHeader = true
	}
	l.ResponseWriter.WriteHeader(code)
}

func loggingWrapper(upstream func(*logrus.Entry, http.ResponseWriter, *http.Request, httprouter.Params)) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		l, w, f := logFor(r, w)
		defer f()
		upstream(l, w, r, p)
	}
}

func simpleLoggingWrapper(upstream httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		_, w, f := logFor(r, w)
		defer f()
		upstream(w, r, p)
	}
}

func logFor(r *http.Request, w http.ResponseWriter) (l *logrus.Entry, _ http.ResponseWriter, toDefer func()) {
	l = logrus.WithFields(logrus.Fields{"UID": uuid.NewV1().String(), "path": r.URL.Path, "method": r.Method})
	loggingWriter := &statusCodeCapturingResponseWriter{w, false, 200}
	start := time.Now()
	return l, loggingWriter, func() {
		l = l.WithFields(logrus.Fields{
			"status":   loggingWriter.statusCode,
			"duration": time.Since(start).String(),
		})
		logFunc := l.Debug
		if loggingWriter.statusCode > 499 {
			logFunc = l.Error
		}
		logFunc("responded")
	}
}

type instrumentationWrapper struct {
	*httprouter.Router
	metrics *prometheus.HistogramVec
}

func (iw *instrumentationWrapper) wrap(method, path string, handler httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		capturingWriter := &statusCodeCapturingResponseWriter{w, false, 200}
		start := time.Now()
		handler(capturingWriter, r, p)
		iw.metrics.WithLabelValues(method, path, strconv.Itoa(capturingWriter.statusCode)).Observe(float64(time.Since(start).Milliseconds() / 1000))
	}
}

func (iw *instrumentationWrapper) GET(path string, handle httprouter.Handle) {
	iw.Router.GET(path, iw.wrap("GET", path, handle))
}

func (iw *instrumentationWrapper) POST(path string, handle httprouter.Handle) {
	iw.Router.POST(path, iw.wrap("POST", path, handle))
}

func (iw *instrumentationWrapper) PUT(path string, handle httprouter.Handle) {
	iw.Router.PUT(path, iw.wrap("PUT", path, handle))
}

func (iw *instrumentationWrapper) PATCH(path string, handle httprouter.Handle) {
	iw.Router.PATCH(path, iw.wrap("PATCH", path, handle))
}

func (iw *instrumentationWrapper) DELETE(path string, handle httprouter.Handle) {
	iw.Router.DELETE(path, iw.wrap("DELETE", path, handle))
}

var instrumentationMetrics = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name: "http_server_request_duration_seconds",
}, []string{"method", "path", "status"},
)

func init() {
	// Must happen in init(), otherwise runnig unittests with count > 1 always fails due to
	// duplicate registration
	prometheus.MustRegister(instrumentationMetrics)
}

func newInstrumentedRouter() *instrumentationWrapper {
	iw := &instrumentationWrapper{
		Router:  httprouter.New(),
		metrics: instrumentationMetrics,
	}
	return iw
}
