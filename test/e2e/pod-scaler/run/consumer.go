//go:build e2e
// +build e2e

package run

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"path"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Admission sets up the pod-scaler admission server and returns a transport for talking to it
func Admission(t testhelper.TestingTInterface, dataDir, kubeconfig string, parent context.Context, stream bool) (string, *http.Transport) {
	authDir := t.TempDir()
	caCertFile := path.Join(authDir, "ca.crt")
	caKeyFile := path.Join(authDir, "ca.key")
	caSerialFile := path.Join(authDir, "ca.serial")
	ca, err := crypto.MakeSelfSignedCA(caCertFile, caKeyFile, caSerialFile, "selfca", 10)
	if err != nil {
		t.Fatalf("Failed to generate self-signed CA for admission: %v", err)
	}
	serverHostname := "127.0.0.1"
	serverCertFile := path.Join(authDir, "tls.crt")
	serverKeyFile := path.Join(authDir, "tls.key")
	if _, _, err := ca.EnsureServerCert(serverCertFile, serverKeyFile, sets.NewString(serverHostname), 10); err != nil {
		t.Fatalf("Failed to ensure server cert and key for admission: %v", err)
	}
	clientCertFile := path.Join(authDir, "client.crt")
	clientKeyFile := path.Join(authDir, "client.key")
	clientTLSConfig, _, err := ca.EnsureClientCertificate(clientCertFile, clientKeyFile, &user.DefaultInfo{
		Name: "/CN=admission-webhook.pod-scaler.svc",
	}, 10)
	if err != nil {
		t.Fatalf("Failed to ensure client cert and key for admission: %v", err)
	}

	podScalerFlags := []string{
		"--loglevel=info",
		"--log-style=text",
		"--cache-dir", dataDir,
		"--mode=consumer.admission",
		"--mutate-resource-limits",
		"--serving-cert-dir=" + authDir,
		"--metrics-port=9092",
	}
	podScaler := testhelper.NewAccessory("pod-scaler", podScalerFlags, func(port, healthPort string) []string {
		t.Logf("pod-scaler admission starting on port %s", port)
		return []string{"--port", port, "--health-port", healthPort}
	}, func(port, healthPort string) []string {
		return []string{port}
	}, clientcmd.RecommendedConfigPathEnvVar+"="+kubeconfig)
	podScaler.RunFromFrameworkRunner(t, parent, stream)
	podScalerHost := "https://" + serverHostname + ":" + podScaler.ClientFlags()[0]
	t.Logf("pod-scaler admission is running at %s", podScalerHost)
	podScaler.Ready(t)

	var certs []tls.Certificate
	for _, cert := range clientTLSConfig.Certs {
		certs = append(certs, tls.Certificate{
			Certificate: [][]byte{cert.Raw},
			PrivateKey:  clientTLSConfig.Key,
			Leaf:        cert,
		})
	}
	pool := x509.NewCertPool()
	for _, cert := range ca.Config.Certs {
		pool.AddCert(cert)
	}
	return podScalerHost, &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: certs,
			RootCAs:      pool,
		},
	}
}

// UI sets up the pod-scaler UI server and returns the host it's serving on
func UI(t testhelper.TestingTInterface, dataDir string, parent context.Context, stream bool) string {
	serverHostname := "127.0.0.1"
	podScalerFlags := []string{
		"--loglevel=info",
		"--log-style=text",
		"--cache-dir", dataDir,
		"--mode=consumer.ui",
		"--data-dir", t.TempDir(),
		"--metrics-port=9093",
	}
	podScaler := testhelper.NewAccessory("pod-scaler", podScalerFlags, func(port, healthPort string) []string {
		t.Logf("pod-scaler admission starting on port %s", port)
		return []string{"--ui-port", port, "--health-port", healthPort}
	}, func(port, healthPort string) []string {
		return []string{port}
	})
	podScaler.RunFromFrameworkRunner(t, parent, stream)
	podScalerHost := "http://" + serverHostname + ":" + podScaler.ClientFlags()[0]
	t.Logf("pod-scaler UI is running at %s", podScalerHost)
	podScaler.Ready(t, func(o *testhelper.ReadyOptions) { o.WaitFor = 200 })
	return podScalerHost
}
