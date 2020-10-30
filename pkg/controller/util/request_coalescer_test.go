package util

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileRequestCoalescer(t *testing.T) {
	testCases := []struct {
		name           string
		coalescerMod   func(*reconcileRequestCoalescer)
		expectedResult reconcile.Result
		expectedError  string
	}{
		{
			name:           "Coalescer hit cache, RequeueAfter is returned",
			expectedResult: reconcile.Result{RequeueAfter: time.Duration(1)},
		},
		{
			name: "Cache miss, Requeue is returned",
			coalescerMod: func(rc *reconcileRequestCoalescer) {
				rc.upstream = reconcile.Func(func(_ reconcile.Request) (reconcile.Result, error) {
					return reconcile.Result{Requeue: true}, nil
				})
				rc.coalesceWindow = time.Duration(0)
			},
			expectedResult: reconcile.Result{Requeue: true},
		},
		{
			name: "Cache miss, RequeueAfter is returned",
			coalescerMod: func(rc *reconcileRequestCoalescer) {
				rc.upstream = reconcile.Func(func(_ reconcile.Request) (reconcile.Result, error) {
					return reconcile.Result{RequeueAfter: time.Duration(3)}, nil
				})
				rc.coalesceWindow = time.Duration(0)
			},
			expectedResult: reconcile.Result{RequeueAfter: time.Duration(3)},
		},
		{
			name: "Cache miss, Error is returned",
			coalescerMod: func(rc *reconcileRequestCoalescer) {
				rc.upstream = reconcile.Func(func(_ reconcile.Request) (reconcile.Result, error) {
					return reconcile.Result{}, errors.New("some-err")
				})
				rc.coalesceWindow = time.Duration(0)
			},
			expectedError: "some-err",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rcInterfaced := NewReconcileRequestCoalescer(
				reconcile.Func(func(_ reconcile.Request) (reconcile.Result, error) {
					return reconcile.Result{}, nil
				}),
				time.Duration(2),
			)
			rc := rcInterfaced.(*reconcileRequestCoalescer)
			rc.timeSince = func(_ time.Time) time.Duration { return time.Duration(1) }

			if tc.coalescerMod != nil {
				tc.coalescerMod(rc)
			}

			result, err := rc.Reconcile(reconcile.Request{})
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			if diff := cmp.Diff(errMsg, tc.expectedError); diff != "" {
				t.Errorf("error differs from expected error: %s", diff)
			}
			if diff := cmp.Diff(result, tc.expectedResult); diff != "" {
				t.Errorf("result differs from expectedResult: %s", diff)
			}
		})
	}
}

func TestReconcileRequestCoalescer_threadSafety(t *testing.T) {
	reconciler := NewReconcileRequestCoalescer(
		reconcile.Func(func(_ reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
		time.Duration(1),
	)

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() { _, _ = reconciler.Reconcile(reconcile.Request{}); wg.Done() }()
	go func() { _, _ = reconciler.Reconcile(reconcile.Request{}); wg.Done() }()
	wg.Wait()
}
