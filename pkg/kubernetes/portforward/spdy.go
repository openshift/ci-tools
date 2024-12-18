package portforward

import (
	"net/http"
	"net/url"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

func SPDYPortForwarder(method string, url *url.URL, readyChannel chan struct{}, opts PortForwardOptions) error {
	transport, upgrader, err := spdy.RoundTripperFor(opts.Config)
	if err != nil {
		return err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, method, url)
	fw, err := portforward.NewOnAddresses(dialer, opts.Address, opts.Ports, opts.StopChannel, readyChannel, opts.Out, opts.ErrOut)
	if err != nil {
		return err
	}
	return fw.ForwardPorts()
}
