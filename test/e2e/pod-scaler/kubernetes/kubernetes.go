// +build e2e

package kubernetes

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Fake serves fake data as if it were a k8s apiserver
func Fake(t testhelper.TestingTInterface, tmpDir, prometheusAddr string) string {
	k8sPort := testhelper.GetFreePort(t)
	k8sAddr := "0.0.0.0:" + k8sPort
	mux := http.NewServeMux()
	mux.HandleFunc("/apis/route.openshift.io/v1/namespaces/openshift-monitoring/routes/prometheus-k8s", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if _, err := writer.Write([]byte(fmt.Sprintf(`{"spec":{"host":"%s"}}`, prometheusAddr))); err != nil {
			http.Error(writer, err.Error(), 500)
			return
		}
	})
	server := &http.Server{
		Addr:    k8sAddr,
		Handler: mux,
	}
	interrupts.ListenAndServe(server, 10*time.Second)

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
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {
				Username: "user",
				Password: "pass",
			},
		},
	}

	kubeconfigFile, err := ioutil.TempFile(tmpDir, "kubeconfig")
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
	return kubeconfigFile.Name()
}
