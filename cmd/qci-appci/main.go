package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
)

type options struct {
	listenAddr  string
	gracePeriod time.Duration
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8400", "The address the proxy shall listen on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func main() {
	logrusutil.ComponentInit()
	logrus.SetLevel(logrus.DebugLevel)
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get opts")
	}

	server, err := createProxyServer(opts.listenAddr)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create server")
	}

	interrupts.ListenAndServe(server, opts.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}

func createProxyServer(listenAddr string) (*http.Server, error) {
	repoURL, err := url.Parse("https://quay.io")
	if err != nil {
		return nil, fmt.Errorf("failed to parse qci-appci's url: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(repoURL)
	return &http.Server{
		Addr:    listenAddr,
		Handler: getRouter(proxy),
	}, nil
}

func getRouter(proxy *httputil.ReverseProxy) *http.ServeMux {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("Authorization")
		nonEmptyToken := v != "" && strings.HasPrefix(v, "Bearer ") && strings.TrimPrefix(v, "Bearer ") != ""
		l := logrus.WithFields(logrus.Fields{
			"method":   r.Method,
			"path":     r.URL.Path,
			"hasToken": nonEmptyToken,
		})
		l.Debug("Received request")
		proxy.ServeHTTP(w, r)
	})
	return handler
}
