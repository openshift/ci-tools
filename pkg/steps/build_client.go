package steps

import (
	"io"

	"k8s.io/client-go/rest"

	buildapi "github.com/openshift/api/build/v1"
	"github.com/openshift/client-go/build/clientset/versioned/scheme"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
)

type BuildClient interface {
	buildclientset.BuildInterface
	Logs(name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error)
}

type buildClient struct {
	buildclientset.BuildInterface

	client    rest.Interface
	namespace string
}

func NewBuildClient(client buildclientset.BuildInterface, restClient rest.Interface, namespace string) BuildClient {
	return &buildClient{
		BuildInterface: client,
		client:         restClient,
		namespace:      namespace,
	}
}

func (c *buildClient) Logs(name string, options *buildapi.BuildLogOptions) (io.ReadCloser, error) {
	return c.client.Get().
		Namespace(c.namespace).
		Name(name).
		Resource("builds").
		SubResource("log").
		VersionedParams(options, scheme.ParameterCodec).
		Stream()
}
