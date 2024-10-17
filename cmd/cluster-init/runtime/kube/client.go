package kube

import (
	"fmt"
	"net/http"

	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	httpruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/http"
)

func NewClient(config *rest.Config) (ctrlruntimeclient.Client, error) {
	if runtime.IsIntegrationTest() {
		config.Wrap(func(rt http.RoundTripper) http.RoundTripper { return httpruntime.ReplayTransport(rt) })
		config.AcceptContentTypes = "application/json, */*"
		config.ContentType = "application/json"
		client, err := rest.HTTPClientFor(config)
		if err != nil {
			return nil, fmt.Errorf("http client: %w", err)
		}
		return ctrlruntimeclient.New(config, ctrlruntimeclient.Options{HTTPClient: client})
	}
	return ctrlruntimeclient.New(config, ctrlruntimeclient.Options{})
}
