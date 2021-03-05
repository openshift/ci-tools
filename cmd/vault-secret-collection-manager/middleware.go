package main

import (
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
)

type loggingResponseWriter struct {
	http.ResponseWriter
	l           *logrus.Entry
	wroteHeader bool
	statusCode  int
}

func (l *loggingResponseWriter) Write(p []byte) (n int, err error) {
	l.wroteHeader = true
	return l.ResponseWriter.Write(p)
}

func (l *loggingResponseWriter) WriteHeader(code int) {
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
	loggingWriter := &loggingResponseWriter{w, l, false, 200}
	start := time.Now()
	return l, loggingWriter, func() {
		loggingWriter.l = loggingWriter.l.WithFields(logrus.Fields{
			"status":   loggingWriter.statusCode,
			"duration": time.Since(start).String(),
		})
		logFunc := loggingWriter.l.Debug
		if loggingWriter.statusCode > 499 {
			logFunc = loggingWriter.l.Error
		}
		logFunc("responded")
	}
}
