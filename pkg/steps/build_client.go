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
	NodeArchitectures() []string
	ManifestToolDockerCfg() string
	LocalRegistryDNS() string
}

type buildClient struct {
	loggingclient.LoggingClient
	client                rest.Interface
	nodeArchitectures     []string
	manifestToolDockerCfg string
	localRegistryDNS      string
}

func NewBuildClient(client loggingclient.LoggingClient, restClient rest.Interface, nodeArchitectures []string, manifestToolDockerCfg, localRegistryDNS string) BuildClient {
	return &buildClient{
		LoggingClient:         client,
		client:                restClient,
		nodeArchitectures:     nodeArchitectures,
		manifestToolDockerCfg: manifestToolDockerCfg,
		localRegistryDNS:      localRegistryDNS,
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

func (c *buildClient) NodeArchitectures() []string {
	return c.nodeArchitectures
}

func (c *buildClient) ManifestToolDockerCfg() string {
	return c.manifestToolDockerCfg
}

func (c *buildClient) LocalRegistryDNS() string {
	return c.localRegistryDNS
}
