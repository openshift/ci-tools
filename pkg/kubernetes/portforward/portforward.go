package portforward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

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

// Run leverages forwarder to create a TCP tunnel according to opts. This function runs asynchronously
// and returns a channel that may:
// - produce and error and then close immediately. This means the forwarder couldn't establish the tunnel;
// - close without errors. In this case the tunnel is in place and ready to accept connections.
//
// A client is not supposed to close the channel but it must wait for it to either close or
// produce an error; this function will leak a goroutine otherwise.
//
// Good:
//
//	if err := <-Run(ctx, PortForwardOptions{}); err != nil {
//		// handle the error somehow
//	}
//
// Bad:
//
//	_ = Run(ctx, PortForwardOptions{}) // a goroutine will run forever, trying to push an error
func Run(ctx context.Context, forwarder PortForwarder, opts PortForwardOptions) chan error {
	errChannel := make(chan error)
	sendErr := func(err error) {
		go func() {
			errChannel <- err
			close(errChannel)
		}()
	}

	readyChannel := make(chan struct{})

	podGetter, err := podGetterOrDefault(opts)
	if err != nil {
		sendErr(fmt.Errorf("pod getter: %w", err))
		return errChannel
	}

	pod, err := podGetter(ctx, opts.Namespace, opts.PodName)
	if err != nil {
		sendErr(fmt.Errorf("get pod %s/%s: %w", opts.Namespace, opts.PodName, err))
		return errChannel
	}

	if pod.Status.Phase != corev1.PodRunning {
		sendErr(fmt.Errorf("pod is not running - current status=%v", pod.Status.Phase))
		return errChannel
	}

	restClient, err := restClientFor(opts.Config)
	if err != nil {
		sendErr(fmt.Errorf("new rest client: %w", err))
		return errChannel
	}
	req := restClient.Post().Resource("pods").Namespace(opts.Namespace).Name(opts.PodName).SubResource("portforward")

	// ForwardPorts runs into its own goroutine, therefore error checking might race.
	var fwErr error
	m := sync.Mutex{}
	setFwErr := func(e error) {
		m.Lock()
		fwErr = e
		m.Unlock()
	}
	getFwErr := func() error {
		m.Lock()
		defer m.Unlock()
		return fwErr
	}

	go func() {
		err := forwarder("POST", req.URL(), readyChannel, opts)
		setFwErr(err)
		// forwarder might return an error before it has any chance of closing the ready channel.
		// We have to detect this case and close it ourselves to avoid a deadlock.
		if _, ok := <-readyChannel; ok {
			close(readyChannel)
		}
	}()

	<-readyChannel

	sendErr(getFwErr())
	return errChannel
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
