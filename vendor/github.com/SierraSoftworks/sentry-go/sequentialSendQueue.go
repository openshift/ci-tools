package sentry

import (
	"sync"

	"github.com/pkg/errors"
)

// NewSequentialSendQueue creates a new sequential send queue instance with
// a  given buffer size which can be used as a replacement for the default
// send queue.
func NewSequentialSendQueue(buffer int) SendQueue {
	b := make(chan QueuedEventInternal, buffer)
	q := &sequentialSendQueue{
		buffer:     b,
		shutdownCh: make(chan struct{}),
	}

	q.wait.Add(1)
	go q.worker(b)
	return q
}

type sequentialSendQueue struct {
	buffer     chan<- QueuedEventInternal
	shutdown   bool
	shutdownCh chan struct{}

	wait sync.WaitGroup
}

func (q *sequentialSendQueue) Enqueue(cfg Config, packet Packet) QueuedEvent {
	e := NewQueuedEvent(cfg, packet)
	ei := e.(QueuedEventInternal)

	if q.shutdown {
		err := errors.New("sequential send queue: shutdown")
		ei.Complete(errors.Wrap(err, ErrSendQueueShutdown.Error()))
		return e
	}

	select {
	case q.buffer <- ei:
	default:
		if e, ok := e.(QueuedEventInternal); ok {
			err := errors.New("sequential send queue: buffer full")
			e.Complete(errors.Wrap(err, ErrSendQueueFull.Error()))
		}
	}

	return e
}

func (q *sequentialSendQueue) Shutdown(wait bool) {
	if q.shutdown {
		return
	}

	q.shutdownCh <- struct{}{}
	q.shutdown = true
	if wait {
		q.wait.Wait()
	}
}

func (q *sequentialSendQueue) worker(buffer <-chan QueuedEventInternal) {
	defer q.wait.Done()

	for {
		select {
		case <-q.shutdownCh:
			return
		case e, ok := <-buffer:
			if !ok {
				return
			}

			cfg := e.Config()
			t := cfg.Transport()
			if t == nil {
				e.Complete(errors.New("no transport configured"))
				continue
			}

			err := t.Send(cfg.DSN(), e.Packet())
			e.Complete(err)
		}
	}
}
