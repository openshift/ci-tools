//go:build e2e_framework
// +build e2e_framework

package framework

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

// Alias this for compatibility
type T = testhelper.T

// we want to ensure that everything runs in parallel and that users of this framework
// do not need to explicitly remember to call the top-level testing.T method, so we do
// it for them and keep track of which we've already done so we do not cause a double-
// call panic
var seen = sync.Map{}

type TestFunc func(t *T, cmd *CiOperatorCommand)

// Run mimics the testing.T.Run function while providing a nice set of concurrency
// guarantees for the processes that we create and manage for test cases. We ensure:
//   - the ci-operator process is interrupted (SIGINT) before the test times out, so
//     that it has time to clean up and create artifacts before the test exits
//   - any accessory processes that are started will only be exposed to the ci-operator
//     command once they have signalled that they are healthy and ready
//   - any accessory processes will have their lifetime bound to the lifetime of the
//     individual test case - they will be killed (SIGKILL) when the test finishes
//   - any errors in running an accessory process (other than it being killed by the
//     above mechanism) will be fatal to the test execution and will preempt the other
//     test routines
//
// This is a generally non-standard use of the testing.T construct; the two largest
// reasons we built this abstraction are that we need to manage a fairly complex and
// fragile set of concurrency and lifetime guarantees for the processes that are
// executed for each test case. We must control the scope in which each test runs, to
// be able to defer() actions that cancel background processes when a test finishes
// execution, as child goroutines are not by default killed when a test finished and
// interaction with the parent testing.T after the test is finished causes a panic.
// Furthermore, we could not simply use the central testing.T for managing control
// flow as it is not allowed to call testing.T.FailNow (via Fatal, etc) in anything
// other than the main testing goroutine. Therefore, when more than one routine needs
// to be able to influence the execution flow (e.g. preempt other routines) we must
// have the central routine watch for incoming errors from delegate routines.
func Run(top *testing.T, name string, f TestFunc, accessories ...*Accessory) {
	if _, previouslyCalled := seen.LoadOrStore(fmt.Sprintf("%p", top), nil); !previouslyCalled {
		top.Parallel()
	}
	top.Run(name, func(mid *testing.T) {
		mid.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		bottom := testhelper.NewT(ctx, mid)
		cmd := newCiOperatorCommand(bottom)
		testDone, cleanupDone := make(chan struct{}), make(chan struct{})
		defer func() {
			// signal to the command that we no longer need to be waiting to
			// interrupt it; then wait for the cleanup routine to finish before
			// we consider the test done
			close(testDone)
			<-cleanupDone
		}()
		cmd.testDone = testDone
		cmd.cleanupDone = cleanupDone

		wg := sync.WaitGroup{}
		wg.Add(len(accessories))
		for _, accessory := range accessories {
			// binding the accessory to ctx ensures its lifetime is only
			// as long as the test we are running in this specific case
			accessory.RunFromFrameworkRunner(bottom, ctx, false)
			cmd.AddArgs(accessory.ClientFlags()...)
			go func(a *Accessory) {
				defer wg.Done()
				a.Ready(bottom)
			}(accessory)
		}
		wg.Wait()

		go func() {
			defer func() { cancel() }() // stop waiting for errors
			f(bottom, &cmd)
		}()

		bottom.Wait()
	})
}
