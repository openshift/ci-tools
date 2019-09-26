package coalescer

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	sharedVar := 0
	wg := &sync.WaitGroup{}
	c := NewCoalescer(func() error {
		fmt.Printf("Hello\n")
		time.Sleep(time.Millisecond * 250)
		sharedVar++
		return nil
	})
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			c.Run()
			wg.Done()
		}()
	}
	wg.Wait()
	if sharedVar != 1 {
		t.Errorf("Shared var should equal 1, but instead equals %d", sharedVar)
	}
}
