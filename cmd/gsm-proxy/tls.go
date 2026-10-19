package main

import (
	"context"
	"crypto/tls"
	"net/http"

	"github.com/bombsimon/logrusr/v3"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	controllerruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/prow/pkg/interrupts"
)

// tlsServer preserves Prow's graceful shutdown while serving a reloadable certificate.
type tlsServer struct {
	*http.Server
	watcher *certwatcher.CertWatcher
}

// newTLSServer requires a valid initial key pair so misconfigured pods never serve HTTP.
func newTLSServer(server *http.Server, certFile, keyFile string) (*tlsServer, error) {
	controllerruntimelog.SetLogger(logrusr.New(logrus.StandardLogger()))
	watcher, err := certwatcher.New(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	server.TLSConfig = &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: watcher.GetCertificate,
	}
	return &tlsServer{Server: server, watcher: watcher}, nil
}

// ListenAndServe satisfies Prow's server interface without opening a plaintext listener.
func (s *tlsServer) ListenAndServe() error {
	interrupts.Run(s.watchCertificate)
	return s.Server.ListenAndServeTLS("", "")
}

// watchCertificate stops the process if certificate watching cannot start, rather than
// silently serving a certificate that will eventually expire without renewal.
func (s *tlsServer) watchCertificate(ctx context.Context) {
	if err := s.watcher.Start(ctx); err != nil && ctx.Err() == nil {
		logrus.WithError(err).Error("TLS certificate watcher stopped")
		interrupts.Terminate()
	}
}
