//go:build e2e
// +build e2e

package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	buildv1 "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Options define what our k8s fake will do
type Options struct {
	Patterns map[string]http.HandlerFunc
}

// Option mutates the options to enable or disable functionality
type Option func(*Options)

// Prometheus adds a handler for clients looking for the prometheus route
func Prometheus(addr string) Option {
	return func(options *Options) {
		options.Patterns["/apis/route.openshift.io/v1/namespaces/openshift-monitoring/routes/prometheus-k8s"] = func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if _, err := writer.Write([]byte(fmt.Sprintf(`{"spec":{"host":"%s"}}`, addr))); err != nil {
				http.Error(writer, err.Error(), 500)
				return
			}
		}
	}
}

// Builds adds handlers for all the builds provided. The mapping is namespace -> name -> labels
func Builds(labelsByNamespacedName map[string]map[string]map[string]string) Option {
	return func(options *Options) {
		for namespace, names := range labelsByNamespacedName {
			for name, labels := range names {
				namespace, name, labels := namespace, name, labels
				options.Patterns[fmt.Sprintf("/apis/build.openshift.io/v1/namespaces/%s/builds/%s", namespace, name)] = func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", "application/json")
					build := &buildv1.Build{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Build",
							APIVersion: "build.openshift.io/v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Namespace: namespace,
							Name:      name,
							Labels:    labels,
						},
					}
					raw, err := json.Marshal(build)
					if err != nil {
						http.Error(writer, err.Error(), 500)
						return
					}
					if _, err := writer.Write(raw); err != nil {
						http.Error(writer, err.Error(), 500)
						return
					}
				}
			}
		}
	}
}

// Fake serves fake data as if it were a k8s apiserver
func Fake(t testhelper.TestingTInterface, tmpDir string, options ...Option) string {
	readyPath := "/healthz/ready"
	o := Options{Patterns: map[string]http.HandlerFunc{
		readyPath: func(w http.ResponseWriter, _ *http.Request) {},
	}}
	for _, option := range options {
		option(&o)
	}
	k8sPort := testhelper.GetFreePort(t)
	k8sAddr := "0.0.0.0:" + k8sPort
	mux := http.NewServeMux()
	for pattern, handler := range o.Patterns {
		mux.HandleFunc(pattern, handler)
	}
	server := &http.Server{
		Addr:    k8sAddr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("kubernetes server failed to listen: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("failed to close kubernetes server: %v", err)
		}
	})

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {
				Server: k8sAddr,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:   "default",
				AuthInfo:  "user",
				Namespace: "ns",
			},
		},
		CurrentContext: "default",
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Username: "user",
				Password: "pass",
			},
		},
	}

	kubeconfigFile, err := os.CreateTemp(tmpDir, "kubeconfig")
	if err != nil {
		t.Fatalf("Failed to create temporary kubeconfig file: %v", err)
	}
	if err := kubeconfigFile.Close(); err != nil {
		t.Fatalf("Failed to close temporary kubeconfig file: %v", err)
	}
	if err := clientcmd.ModifyConfig(&clientcmd.PathOptions{
		GlobalFile: kubeconfigFile.Name(),
		LoadingRules: &clientcmd.ClientConfigLoadingRules{
			ExplicitPath: kubeconfigFile.Name(),
		},
	}, kubeconfig, false); err != nil {
		t.Fatalf("Failed to write temporary kubeconfig file: %v", err)
	}
	testhelper.WaitForHTTP200(fmt.Sprintf("http://127.0.0.1:%s%s", k8sPort, readyPath), "kubernetes server", 90, t)
	return kubeconfigFile.Name()
}
