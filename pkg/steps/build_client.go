package steps

import (
	"context"
	"io"

	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/client-go/build/clientset/versioned/scheme"
)

type BuildClient interface {
	ctrlruntimeclient.Client
	Logs(namespace, name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error)
}

type buildClient struct {
	ctrlruntimeclient.Client
	client rest.Interface
}

func NewBuildClient(client ctrlruntimeclient.Client, restClient rest.Interface) BuildClient {
	return &buildClient{
		Client: client,
		client: restClient,
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
