package coalescer

import (
	"sync"
)

// Coalescer contains a Run function that will run a specified function.
// If the function is already being run in a separate thread, the Coalescer
// will simply wait until the function finishes and return nil
type Coalescer interface {
	Run() error
}

// NewCoalescer returns a new Coalescer with the given function set as the runFunc.
func NewCoalescer(f func() error) Coalescer {
	return &coalescer{runFunc: f, once: &sync.Once{}}
}

type coalescer struct {
	sync.Mutex
	once    *sync.Once
	runFunc func() error
}

// Run the runFunc. If the runFunc is currently being run in another thread, wait
// and return nil when the the other thread finishes.
func (c *coalescer) Run() error {
	var err error
	var once *sync.Once

	c.Lock()
	once = c.once
	c.Unlock()
	once.Do(func() {
		defer func() {
			c.Lock()
			c.once = &sync.Once{}
			c.Unlock()
		}()
		err = c.runFunc()
		if err != nil {
			return
		}
	})
	return err
}
