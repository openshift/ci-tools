package kubernetes

import (
	"fmt"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

type PodClient interface {
	loggingclient.LoggingClient
	// WithNewLoggingClient returns a new instance of the PodClient that resets
	// its LoggingClient.
	WithNewLoggingClient() PodClient
	Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error)
	GetLogs(namespace, name string, opts *coreapi.PodLogOptions) *rest.Request
}

func NewPodClient(ctrlclient loggingclient.LoggingClient, config *rest.Config, client rest.Interface) PodClient {
	return &podClient{LoggingClient: ctrlclient, config: config, client: client}
}

type podClient struct {
	loggingclient.LoggingClient
	config *rest.Config
	client rest.Interface
}

func (c podClient) Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error) {
	u := c.client.Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").VersionedParams(opts, scheme.ParameterCodec).URL()
	e, err := remotecommand.NewSPDYExecutor(c.config, "POST", u)
	if err != nil {
		return nil, fmt.Errorf("could not initialize a new SPDY executor: %w", err)
	}
	return e, nil
}

func (c podClient) GetLogs(namespace, name string, opts *coreapi.PodLogOptions) *rest.Request {
	return c.client.Get().Namespace(namespace).Name(name).Resource("pods").SubResource("log").VersionedParams(opts, scheme.ParameterCodec)
}

func (c podClient) WithNewLoggingClient() PodClient {
	return podClient{
		LoggingClient: c.New(),
		config:        c.config,
		client:        c.client,
	}
}
