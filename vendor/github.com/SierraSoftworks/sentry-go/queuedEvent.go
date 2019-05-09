package sentry

// A QueuedEvent allows you to track the status of sending
// an event to Sentry.
type QueuedEvent interface {
	EventID() string
	Wait() QueuedEvent
	WaitChannel() <-chan error
	Error() error
}

// QueuedEventInternal is an interface used by SendQueue
// implementations to "complete" a queued event once it has
// either been sent to Sentry, or sending failed with an error.
type QueuedEventInternal interface {
	QueuedEvent
	Packet() Packet
	Config() Config
	Complete(err error)
}

// NewQueuedEvent is used by SendQueue implementations to expose
// information about the events that they are sending to Sentry.
func NewQueuedEvent(cfg Config, packet Packet) QueuedEvent {
	e := &queuedEvent{
		cfg:    cfg,
		packet: packet,
		wait:   make(chan struct{}),
	}

	return e
}

type queuedEvent struct {
	cfg    Config
	packet Packet
	err    error

	wait chan struct{}
}

func (e *queuedEvent) EventID() string {
	if packet, ok := e.packet.(*packet); ok {
		return packet.getEventID()
	}

	return ""
}

func (e *queuedEvent) Wait() QueuedEvent {
	_, _ = <-e.wait

	return e
}

func (e *queuedEvent) WaitChannel() <-chan error {
	ch := make(chan error)

	go func() {
		_, _ = <-e.wait

		if e.err != nil {
			ch <- e.err
		}
		close(ch)
	}()

	return ch
}

func (e *queuedEvent) Error() error {
	return e.Wait().(*queuedEvent).err
}

func (e *queuedEvent) Packet() Packet {
	return e.packet
}

func (e *queuedEvent) Config() Config {
	return e.cfg
}

func (e *queuedEvent) Complete(err error) {
	select {
	case _, _ = <-e.wait:
	default:
		e.err = err
		close(e.wait)
	}
}
