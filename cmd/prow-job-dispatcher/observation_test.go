package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

// observationTestAuthorizer permits fixture requests without using Kubernetes credentials.
type observationTestAuthorizer struct{}

// Authorize requires a fixture header so all listener routes exercise authentication.
func (observationTestAuthorizer) Authorize(request *http.Request) (int, error) {
	if request.Header.Get("Authorization") != "Bearer projected-token" {
		return 401, errors.New("unauthorized")
	}
	return 200, nil
}

func TestObservationOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "disabled"},
		{name: "enabled", args: []string{"--observation-api-listen-address=:8443"}},
		{name: "invalid listener", args: []string{"--observation-api-listen-address=8443"}, want: "invalid --observation-api-listen-address: address 8443: missing port in address"},
		{name: "missing namespace", args: []string{"--observation-api-listen-address=:8443", "--observation-api-service-account=dashboard"}, want: "--observation-api-service-account must be a valid namespace/name"},
		{name: "empty name", args: []string{"--observation-api-listen-address=:8443", "--observation-api-service-account=ci/"}, want: "--observation-api-service-account must be a valid namespace/name"},
		{name: "invalid namespace", args: []string{"--observation-api-listen-address=:8443", "--observation-api-service-account=CI/dashboard"}, want: "--observation-api-service-account must be a valid namespace/name"},
		{name: "no certificate", args: []string{"--observation-api-listen-address=:8443", "--observation-api-tls-cert-file="}, want: "--observation-api-tls-cert-file and --observation-api-tls-key-file are required"},
		{name: "no key", args: []string{"--observation-api-listen-address=:8443", "--observation-api-tls-key-file="}, want: "--observation-api-tls-cert-file and --observation-api-tls-key-file are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var options observationOptions
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			options.addFlags(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			err := options.validate()
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
			if flags.Lookup("observation-api-token-path") != nil {
				t.Fatal("legacy shared secret flag remains")
			}
		})
	}
}

func TestObservationListenerRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, path, method, token string
		want                      int
	}{
		{name: "state", path: "/observe/v1/state", method: "GET", token: "projected-token", want: 200},
		{name: "metrics", path: "/metrics", method: "GET", token: "projected-token", want: 200},
		{name: "no authentication", path: "/observe/v1/state", method: "GET", want: 401},
		{name: "metrics requires authentication", path: "/metrics", method: "GET", want: 401},
		{name: "control not mounted", path: "/control/v1/plans", method: "GET", token: "projected-token", want: 404},
		{name: "write rejected", path: "/observe/v1/state", method: "POST", token: "projected-token", want: 405},
		{name: "metrics write rejected", path: "/metrics", method: "POST", token: "projected-token", want: 405},
		{name: "event not mounted", path: "/event", method: "GET", token: "projected-token", want: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := observationOptions{listenAddress: ":8443"}
			server := options.server(dispatcher.NewSnapshotManager(""), nil, observationTestAuthorizer{})
			request := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Errorf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
			if server.TLSConfig == nil || server.TLSConfig.GetCertificate == nil || server.MaxHeaderBytes == 0 || server.ReadHeaderTimeout == 0 {
				t.Error("missing TLS configuration or request limits")
			}
		})
	}
}

func TestSchedulingListenerRetiresObservation(t *testing.T) {
	for _, tc := range []struct {
		name, path, method string
		want               int
	}{
		{name: "observation GET", path: "/observe/v1/state", method: "GET", want: 404},
		{name: "observation POST", path: "/observe/v1/state", method: "POST", want: 404},
		{name: "health", path: "/healthz", method: "GET", want: 200},
		{name: "metrics", path: "/metrics", method: "GET", want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := newSchedulingMux(dispatcher.NewSnapshotServer(nil, nil, nil, dispatcher.NewSnapshotManager("")), false)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.want {
				t.Errorf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

// observationCertificate creates a local certificate fixture without network access.
func observationCertificate(t *testing.T, serial int64) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "dispatcher.test"}, NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func TestObservationCertificateReload(t *testing.T) {
	for _, tc := range []struct {
		name            string
		remove, invalid bool
		wantSerial      int64
	}{
		{name: "rotated pair", wantSerial: 2},
		{name: "missing pair", remove: true},
		{name: "invalid pair", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			options := observationOptions{certFile: filepath.Join(directory, "tls.crt"), keyFile: filepath.Join(directory, "tls.key")}
			cert, key := observationCertificate(t, 1)
			if err := os.WriteFile(options.certFile, cert, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(options.keyFile, key, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := options.certificate(nil); err != nil {
				t.Fatal(err)
			}
			cert, key = observationCertificate(t, 2)
			if tc.invalid {
				cert = []byte("invalid")
			}
			if err := os.WriteFile(options.certFile, cert, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(options.keyFile, key, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.remove {
				if err := os.Remove(options.keyFile); err != nil {
					t.Fatal(err)
				}
			}
			pair, err := options.certificate(nil)
			if tc.remove || tc.invalid {
				if err == nil {
					t.Fatal("invalid or missing replacement did not fail closed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			if parsed.SerialNumber.Int64() != tc.wantSerial {
				t.Errorf("got serial %d want %d", parsed.SerialNumber.Int64(), tc.wantSerial)
			}
		})
	}
}
