package util

import (
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NewReconcileRequestCoalescer returns a reconcile wrapper that will delay new reconcile.Requests
// after a sucessful reconciliation for coalesceWindow time.
// A successful reconciliation is defined as as one where all returned values are empty or nil
func NewReconcileRequestCoalescer(upstream reconcile.Reconciler, coalesceWindow time.Duration) reconcile.Reconciler {
	return &reconcileRequestCoalescer{
		lock:                              &sync.RWMutex{},
		lastSuccessfulReconciliationCache: map[string]time.Time{},
		coalesceWindow:                    coalesceWindow,
		upstream:                          upstream,
		timeSince:                         time.Since,
	}
}

type reconcileRequestCoalescer struct {
	lock                              *sync.RWMutex
	lastSuccessfulReconciliationCache map[string]time.Time
	coalesceWindow                    time.Duration
	upstream                          reconcile.Reconciler
	timeSince                         func(time.Time) time.Duration
}

func (rc *reconcileRequestCoalescer) Reconcile(r reconcile.Request) (reconcile.Result, error) {
	rc.lock.RLock()
	lastSuccessfulReconciliation := rc.lastSuccessfulReconciliationCache[r.String()]
	rc.lock.RUnlock()

	if expiredTime := rc.timeSince(lastSuccessfulReconciliation); expiredTime < rc.coalesceWindow {
		return reconcile.Result{RequeueAfter: time.Duration(rc.coalesceWindow - expiredTime)}, nil
	}

	result, err := rc.upstream.Reconcile(r)
	if !IsReconcileSuccessfull(result, err) {
		return result, err
	}

	// Stamp before acquiring the lock as that might take some time
	successfulReconcileTime := time.Now()
	rc.lock.Lock()
	rc.lastSuccessfulReconciliationCache[r.String()] = successfulReconcileTime
	rc.lock.Unlock()

	return result, err
}

func IsReconcileSuccessfull(result reconcile.Result, err error) bool {
	return err == nil && !result.Requeue && int64(result.RequeueAfter) == 0
}
