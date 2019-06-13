package sentry

// A Client is responsible for letting you interact with the Sentry API.
// You can create derivative clients
type Client interface {
	// With creates a new derivative client with the provided options
	// set as part of its defaults.
	With(options ...Option) Client

	// GetOption allows you to retrieve a specific configuration object
	// by its Class name from this client. It is useful if you are interested
	// in using the client to configure Sentry plugins.
	// If an option with the given className could not be found, nil will
	// be returned.
	GetOption(className string) Option

	// Capture will queue an event for sending to Sentry and return a
	// QueuedEvent object which can be used to keep tabs on when it is
	// actually sent, if you are curious.
	Capture(options ...Option) QueuedEvent
}

var defaultClient = NewClient()

// DefaultClient is a singleton client instance which can be used instead
// of instantiating a new client manually.
func DefaultClient() Client {
	return defaultClient
}

type client struct {
	parent  *client
	options []Option
}

// NewClient will create a new client instance with the provided
// default options and config.
func NewClient(options ...Option) Client {
	return &client{
		parent:  nil,
		options: options,
	}
}

func (c *client) Capture(options ...Option) QueuedEvent {
	p := NewPacket().SetOptions(c.fullDefaultOptions()...).SetOptions(options...)

	return c.SendQueue().Enqueue(c, p)
}

func (c *client) With(options ...Option) Client {
	return &client{
		parent:  c,
		options: options,
	}
}

func (c *client) GetOption(className string) Option {
	var opt Option
	for _, o := range c.fullDefaultOptions() {
		if o == nil {
			continue
		}

		if o.Class() != className {
			continue
		}

		if mergeable, ok := o.(MergeableOption); ok {
			opt = mergeable.Merge(opt)
			continue
		}

		opt = o
	}

	return opt
}

func (c *client) DSN() string {
	opt := c.GetOption("sentry-go.dsn")
	if opt == nil {
		return ""
	}

	dsnOpt, ok := opt.(*dsnOption)
	if !ok {
		// Should never be the case unless someone implements a
		// custom dsn option we don't know how to handle
		return ""
	}

	return dsnOpt.dsn
}

func (c *client) Transport() Transport {
	opt := c.GetOption("sentry-go.transport")
	if opt == nil {
		// Should never be the case, we have this set as a base default
		return newHTTPTransport()
	}

	transOpt, ok := opt.(*transportOption)
	if !ok {
		// Should never be the case unless someone implements their own custom
		// transport option that we don't know how to handle.
		return newHTTPTransport()
	}

	return transOpt.transport
}

func (c *client) SendQueue() SendQueue {
	opt := c.GetOption("sentry-go.sendqueue")
	if opt == nil {
		// Should never be the case, we have this set as a base default
		return NewSequentialSendQueue(100)
	}

	sqOpt, ok := opt.(*sendQueueOption)
	if !ok {
		// Should never be the case unless someone implements their own custom
		// sendqueue option that we don't know how to handle.
		return NewSequentialSendQueue(100)
	}

	return sqOpt.queue
}

func (c *client) fullDefaultOptions() []Option {
	if c.parent == nil {
		rootOpts := []Option{}
		for _, provider := range defaultOptionProviders {
			opt := provider()
			if opt != nil {
				rootOpts = append(rootOpts, opt)
			}
		}

		return append(rootOpts, c.options...)
	}

	return append(c.parent.fullDefaultOptions(), c.options...)
}
