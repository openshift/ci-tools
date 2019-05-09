package sentry

// A SendQueue is used by the Sentry client to coordinate the transmission
// of events. Custom queues can be used to control parallelism and circuit
// breaking as necessary.
type SendQueue interface {
	// Enqueue is called by clients wishing to send an event to Sentry.
	// It is provided with a Config and Packet and is expected to return
	// a QueuedEvent compatible object which an application can use to
	// access information about whether the event was sent successfully
	// or not.
	Enqueue(conf Config, packet Packet) QueuedEvent

	// Shutdown is called by a client that wishes to stop the flow of
	// events through a SendQueue.
	Shutdown(wait bool)
}

const (
	// ErrSendQueueFull is used when an attempt to enqueue a
	// new event fails as a result of no buffer space being available.
	ErrSendQueueFull = ErrType("sentry: send queue was full")

	// ErrSendQueueShutdown is used when an attempt to enqueue
	// a new event fails as a result of the queue having been shutdown
	// already.
	ErrSendQueueShutdown = ErrType("sentry: send queue was shutdown")
)

func init() {
	AddDefaultOptions(UseSendQueue(NewSequentialSendQueue(100)))
}

// UseSendQueue allows you to specify the send queue that will be used
// by a client.
func UseSendQueue(queue SendQueue) Option {
	if queue == nil {
		return nil
	}

	return &sendQueueOption{queue}
}

type sendQueueOption struct {
	queue SendQueue
}

func (o *sendQueueOption) Class() string {
	return "sentry-go.sendqueue"
}

func (o *sendQueueOption) Omit() bool {
	return true
}
