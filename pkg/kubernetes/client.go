package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/remotecommand"
	toolswatch "k8s.io/client-go/tools/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

type evaluator func(runtime.Object) (bool, error)

// WaitForConditionOnObject uses a watch to wait for a condition to be true on an object.
// When the condition is satisfied or the timeout expires, the object is returned along
// with any errors encountered.
func WaitForConditionOnObject(ctx context.Context, client ctrlruntimeclient.WithWatch, identifier ctrlruntimeclient.ObjectKey, list ctrlruntimeclient.ObjectList, into ctrlruntimeclient.Object, evaluate evaluator, timeout time.Duration) error {
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", identifier.Name).String()
			res := list
			return res, client.List(ctx, res, &ctrlruntimeclient.ListOptions{Namespace: identifier.Namespace, Raw: &options})
		},
		WatchFunc: func(options metav1.ListOptions) (i watch.Interface, e error) {
			opts := &ctrlruntimeclient.ListOptions{
				Namespace:     identifier.Namespace,
				FieldSelector: fields.OneTermEqualSelector("metadata.name", identifier.Name),
				Raw:           &options,
			}
			res := list
			return client.Watch(ctx, res, opts)
		},
	}

	// objects in this call here are always expected to exist in advance
	var existsPrecondition toolswatch.PreconditionFunc

	waitForObjectStatus := func(event watch.Event) (bool, error) {
		if event.Type == watch.Deleted {
			return false, fmt.Errorf("%s was deleted", identifier.String())
		}
		// the outer library will handle errors, we have no pod data to review in this case
		if event.Type != watch.Added && event.Type != watch.Modified {
			return false, nil
		}
		return evaluate(event.Object)
	}

	waitTimeout, cancel := toolswatch.ContextWithOptionalTimeout(ctx, timeout)
	defer cancel()
	_, syncErr := toolswatch.UntilWithSync(waitTimeout, lw, into, existsPrecondition, waitForObjectStatus)
	// Hack to make sure this ends up in the logging client
	if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: identifier.Namespace, Name: identifier.Name}, into); err != nil {
		logrus.WithError(err).Debug("failed to get object after finishing watch")
	}
	return syncErr
}

type PodClient interface {
	loggingclient.LoggingClient
	GetPendingTimeout() time.Duration
	// WithNewLoggingClient returns a new instance of the PodClient that resets
	// its LoggingClient.
	WithNewLoggingClient() PodClient
	Exec(namespace, pod string, opts *coreapi.PodExecOptions) (remotecommand.Executor, error)
	GetLogs(namespace, name string, opts *coreapi.PodLogOptions) *rest.Request
}

func NewPodClient(ctrlclient loggingclient.LoggingClient, config *rest.Config, client rest.Interface, pendingTimeout time.Duration) PodClient {
	return &podClient{
		LoggingClient:  ctrlclient,
		config:         config,
		client:         client,
		pendingTimeout: pendingTimeout,
	}
}

type podClient struct {
	loggingclient.LoggingClient
	config         *rest.Config
	client         rest.Interface
	pendingTimeout time.Duration
}

func (c podClient) GetPendingTimeout() time.Duration { return c.pendingTimeout }

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
	c.LoggingClient = c.New()
	return c
}
