package sentry

// Transport is the interface that any network transport must implement
// if it wishes to be used to send Sentry events
type Transport interface {
	Send(dsn string, packet Packet) error
}

// UseTransport allows you to control which transport is used to
// send events for a specific client or packet.
func UseTransport(transport Transport) Option {
	if transport == nil {
		return nil
	}

	return &transportOption{transport}
}

func init() {
	AddDefaultOptions(UseTransport(newHTTPTransport()))
}

type transportOption struct {
	transport Transport
}

func (o *transportOption) Class() string {
	return "sentry-go.transport"
}

func (o *transportOption) Omit() bool {
	return true
}
