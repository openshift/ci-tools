// +build e2e

package run

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"path"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Admission sets up the pod-scaler admission server and returns a transport for talking to it
func Admission(t testhelper.TestingTInterface, dataDir, kubeconfig string, parent context.Context) (string, *http.Transport) {
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
		"--serving-cert-dir=" + authDir,
	}
	podScaler := testhelper.NewAccessory("pod-scaler", podScalerFlags, func(port, _ string) []string {
		t.Logf("pod-scaler admission starting on port %s", port)
		return []string{"--port", port}
	}, func(port, healthPort string) []string {
		return []string{port}
	}, clientcmd.RecommendedConfigPathEnvVar+"="+kubeconfig)
	podScaler.RunFromFrameworkRunner(t, parent)
	podScalerHost := "https://" + serverHostname + ":" + podScaler.ClientFlags()[0]
	t.Logf("pod-scaler admission is running at %s", podScalerHost)
	time.Sleep(time.Second) // TODO: expose health, ready

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
