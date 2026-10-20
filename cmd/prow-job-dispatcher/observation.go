package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/validation"
	authenticationclient "k8s.io/client-go/kubernetes/typed/authentication/v1"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/util"
)

// observationOptions keeps the cluster-only TLS API separate from the scheduling listener.
type observationOptions struct {
	listenAddress  string
	serviceAccount string
	certFile       string
	keyFile        string
}

// addFlags leaves observation disabled until a separate TLS listening address is configured.
func (o *observationOptions) addFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.listenAddress, "observation-api-listen-address", "", "TLS address for read-only observation and metrics, e.g. :8443. Empty disables observation.")
	fs.StringVar(&o.serviceAccount, "observation-api-service-account", "ci/dispatcher-dashboard", "Only namespace/name service account allowed to read observation and metrics.")
	fs.StringVar(&o.certFile, "observation-api-tls-cert-file", "/etc/dispatcher-observation-tls/tls.crt", "TLS certificate for the observation listener, reloaded on each handshake.")
	fs.StringVar(&o.keyFile, "observation-api-tls-key-file", "/etc/dispatcher-observation-tls/tls.key", "TLS private key for the observation listener, reloaded on each handshake.")
}

// validate rejects partial TLS configuration and malformed identities before startup.
func (o *observationOptions) validate() error {
	if o.listenAddress == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(o.listenAddress); err != nil {
		return fmt.Errorf("invalid --observation-api-listen-address: %w", err)
	}
	parts := strings.Split(o.serviceAccount, "/")
	if len(parts) != 2 || len(validation.IsDNS1123Label(parts[0])) != 0 || len(validation.IsDNS1123Subdomain(parts[1])) != 0 {
		return fmt.Errorf("--observation-api-service-account must be a valid namespace/name")
	}
	if o.certFile == "" || o.keyFile == "" {
		return fmt.Errorf("--observation-api-tls-cert-file and --observation-api-tls-key-file are required")
	}
	return nil
}

// certificate loads the mounted pair on each handshake to follow service certificate rotation.
func (o observationOptions) certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certificate, err := tls.LoadX509KeyPair(o.certFile, o.keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading observation TLS certificate: %w", err)
	}
	return &certificate, nil
}

// newServer uses Kubernetes credentials only to review the dashboard's projected token.
func (o observationOptions) newServer(snapshots *dispatcher.SnapshotManager, overrides dispatcher.OverrideReader) (*http.Server, error) {
	if _, err := o.certificate(nil); err != nil {
		return nil, err
	}
	config, err := util.LoadClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("loading observation token review configuration: %w", err)
	}
	client, err := authenticationclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating observation token review client: %w", err)
	}
	parts := strings.Split(o.serviceAccount, "/")
	authorizer := dispatcher.NewServiceAccountAuthorizer(client.TokenReviews(), parts[0], parts[1])
	return o.server(snapshots, overrides, authorizer), nil
}

// server mounts only authenticated read-only handlers on the separate TLS listener.
func (o observationOptions) server(snapshots *dispatcher.SnapshotManager, overrides dispatcher.OverrideReader, authorizer dispatcher.ObservationAuthorizer) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", dispatcher.NewObservationMetricsHandler(authorizer, promhttp.Handler()))
	mux.Handle("/", dispatcher.NewObservationServer(snapshots, overrides, authorizer))
	return &http.Server{
		Addr: o.listenAddress, Handler: mux,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: o.certificate},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10,
	}
}

// serveObservation treats binding or serving failure as fatal instead of exposing a plaintext fallback.
func serveObservation(server *http.Server) {
	logrus.WithError(server.ListenAndServeTLS("", "")).Fatal("observation API stopped")
}
