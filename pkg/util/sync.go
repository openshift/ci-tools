package util

import (
	"runtime"
	"sync"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type WorkerFn func() error

func start(
	n int,
	wg *sync.WaitGroup,
	ch chan<- error,
	produce WorkerFn,
	map_ WorkerFn,
) {
	if n == 0 {
		n = runtime.GOMAXPROCS(0)
	}
	go func() {
		if err := produce(); err != nil {
			ch <- err
		}
	}()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if err := map_(); err != nil {
				ch <- err
			}
		}()
	}
}

func finish(ch <-chan error) error {
	var ret []error
	for err := range ch {
		ret = append(ret, err)
	}
	return utilerrors.NewAggregate(ret)
}

// ProduceMap is similar to ProduceMapReduce but without a reduce step.
func ProduceMap(
	n int,
	produce WorkerFn,
	map_ WorkerFn,
	errCh chan error,
) error {
	var wg sync.WaitGroup
	start(n, &wg, errCh, produce, map_)
	go func() {
		wg.Wait()
		close(errCh)
	}()
	return finish(errCh)
}

// ProduceMapReduce handles the execution of a triphasic pipeline.
// The pipeline is constituted of a single producer, a single reducer, and `n`
// mapper workers, i.e.:
//
//       producer
//      / /   \ \
//     m m  …  m m ---> done
//      \ \   / /        |
//       reducer <-------'
//
// All processes are executed to completion even if an error occurs in any of
// them (as opposed to, for example, `x/sync/errorgroup`).  Errors sent to
// `errCh` are aggregated in the return value.  A process which encounters an
// error can either return it to stop itself or send it to `errCh` and continue
// processing its input.
//
// For convenience, if `n` is zero, it is treated as `runtime.GOMAXPROCS(0)`.
//
// `done` is called when all mapping workers finish.  It usually closes a
// channel used by the reduce step.
func ProduceMapReduce(
	n int,
	produce WorkerFn,
	map_ WorkerFn,
	reduce WorkerFn,
	done func(),
	errCh chan error,
) error {
	var wg sync.WaitGroup
	start(n, &wg, errCh, produce, map_)
	go func() {
		wg.Wait()
		done()
	}()
	go func() {
		if err := reduce(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()
	return finish(errCh)
}
