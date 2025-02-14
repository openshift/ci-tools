package portforward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// PortForwarder knows how to establish a tunnel to a pod at url, according to opts.
// It's likely the implementors of this interface will run async, readyChannel is here to
// inform clients when the tunnel has been successfully established.
type PortForwarder func(method string, url *url.URL, readyChannel chan struct{}, opts PortForwardOptions) error

// PodGetter knows how to get a pod
type PodGetter func(ctx context.Context, namespace, name string) (*corev1.Pod, error)

type PortForwardOptions struct {
	Namespace string
	PodName   string
	PodGetter PodGetter

	// Closing this channel stops the port forwarder
	StopChannel chan struct{}

	Config  *restclient.Config
	Out     io.Writer
	ErrOut  io.Writer
	Address []string
	Ports   []string
}

// FowarderStatus describes the status of the port forwarder once Run has been called.
type FowarderStatus struct {
	// The channel the forwared might report any error to, while it is running. It gets
	// closed right after the error is sent.
	Error chan error
}

// Run leverages forwarder to create a TCP tunnel according to opts. This function runs asynchronously
// and returns a FowarderStatus object. When no error is returned, the caller should assume the forwarder
// has been initialized and it is running. The caller MUST then be listening on the error channel or
// a goroutine will be leakead.
//
// Usage:
//
//	status, err := Run(ctx, forwarder, opts)
//	if err != nil {
//		return fmt.Errorf("run portforwarder: %w", err)
//	}
//	defer func() {
//		close(opts.StopChannel)
//		if err := <-status.ErrChannel; err != nil {
//			return fmt.Errorf("portforwarder: %w", err)
//		}
//	}()
func Run(ctx context.Context, forwarder PortForwarder, opts PortForwardOptions) (FowarderStatus, error) {
	status := FowarderStatus{Error: make(chan error)}
	podGetter, err := podGetterOrDefault(opts)
	if err != nil {
		return status, fmt.Errorf("pod getter: %w", err)
	}

	pod, err := podGetter(ctx, opts.Namespace, opts.PodName)
	if err != nil {
		return status, fmt.Errorf("get pod %s/%s: %w", opts.Namespace, opts.PodName, err)
	}

	if pod.Status.Phase != corev1.PodRunning {
		return status, fmt.Errorf("pod is not running - current status=%v", pod.Status.Phase)
	}

	restClient, err := restClientFor(opts.Config)
	if err != nil {
		return status, fmt.Errorf("new rest client: %w", err)
	}
	req := restClient.Post().Resource("pods").Namespace(opts.Namespace).Name(opts.PodName).SubResource("portforward")

	readyChannel := make(chan struct{})
	go func() {
		err := forwarder("POST", req.URL(), readyChannel, opts)
		// forwarder might return an error before it has any chance of closing the ready channel.
		// We have to detect this case and close it ourselves to avoid a deadlock.
		if _, ok := <-readyChannel; ok {
			close(readyChannel)
		}
		if err != nil {
			status.Error <- err
		}
		close(status.Error)
	}()

	<-readyChannel
	return status, nil
}

func restClientFor(config *restclient.Config) (restclient.Interface, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add to scheme: %w", err)
	}

	gvk, err := apiutil.GVKForObject(&corev1.Pod{}, scheme)
	if err != nil {
		return nil, fmt.Errorf("gvk for pod: %w", err)
	}

	codecs := serializer.NewCodecFactory(scheme)
	return apiutil.RESTClientForGVK(gvk, false, config, codecs, http.DefaultClient)
}

func podGetterOrDefault(opts PortForwardOptions) (PodGetter, error) {
	if opts.PodGetter != nil {
		return opts.PodGetter, nil
	}
	clientset, err := kubernetes.NewForConfig(opts.Config)
	if err != nil {
		return nil, fmt.Errorf("new clientset: %w", err)
	}

	podClient := clientset.CoreV1()
	return func(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
		return podClient.Pods(namespace).Get(ctx, name, v1.GetOptions{})
	}, nil
}
