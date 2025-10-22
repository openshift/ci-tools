package main

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	prowConfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/simplifypath"
)

var (
	uiMetrics = metrics.NewMetrics("repo_init_ui")

	//go:embed frontend/dist
	static embed.FS
)

func serveUI(port, healthPort, metricsPort int) {
	logrusutil.ComponentInit()
	logger := logrus.WithField("component", "repo-init-frontend")

	health := pjutil.NewHealthOnPort(healthPort)
	health.ServeReady()

	metrics.ExposeMetrics("repo-init-ui", prowConfig.PushGateway{}, metricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicking the root
		l(""), // actual UI
	))
	handler := metrics.TraceHandler(simplifier, uiMetrics.HTTPRequestDuration, uiMetrics.HTTPResponseSize)
	mux := http.NewServeMux()
	stripped, err := fs.Sub(static, "frontend/dist")
	if err != nil {
		logger.WithError(err).Fatal("Could not prefix static content.")
	}
	index, err := stripped.Open("index.html")
	if err != nil {
		logger.WithError(err).Fatal("Could not find index.html in static content.")
	}
	indexBytes, err := io.ReadAll(index)
	if err != nil {
		logger.WithError(err).Fatal("Could not read index.html.")
	}
	if err := index.Close(); err != nil {
		logger.WithError(err).Fatal("Could not close index.html.")
	}
	mux.HandleFunc("/", handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(indexBytes); err != nil {
			logrus.WithError(err).Warn("Could not serve index.html.")
		}
	})).ServeHTTP)
	mux.HandleFunc("/static/", handler(http.StripPrefix("/static/", http.FileServer(http.FS(stripped)))).ServeHTTP)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	logger.Debug("Ready to serve HTTP requests.")
}
