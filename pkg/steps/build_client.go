package steps

import (
	"context"
	"io"

	"k8s.io/client-go/rest"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/client-go/build/clientset/versioned/scheme"

	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

type BuildClient interface {
	loggingclient.LoggingClient
	Logs(namespace, name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error)
}

type buildClient struct {
	loggingclient.LoggingClient
	client rest.Interface
}

func NewBuildClient(client loggingclient.LoggingClient, restClient rest.Interface) BuildClient {
	return &buildClient{
		LoggingClient: client,
		client:        restClient,
	}
}

func (c *buildClient) Logs(namespace, name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error) {
	return c.client.Get().
		Namespace(namespace).
		Name(name).
		Resource("builds").
		SubResource("log").
		VersionedParams(options, scheme.ParameterCodec).
		Stream(context.TODO())
}
