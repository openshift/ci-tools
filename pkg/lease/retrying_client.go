package lease

import (
	"context"

	"sigs.k8s.io/boskos/common"
)

// retryingBoskosClient wraps a boskosClient and retries operations that fail
// with 5xx HTTP errors using exponential backoff.  This adds a longer retry
// window (~5 minutes by default) on top of the upstream Boskos client's own
// short retry loop (~14 seconds).
//
// Only 5xx server errors are retried.  Client errors (4xx), sentinel errors
// (ErrNotFound, ErrTypeNotFound), and connection errors (already handled by
// the upstream DialerWithRetry) pass through immediately.
type retryingBoskosClient struct {
	delegate boskosClient
	cfg      retryConfig
}

// withRetry returns a boskosClient that wraps delegate with 5xx retry logic.
func withRetry(delegate boskosClient, cfg retryConfig) boskosClient {
	return &retryingBoskosClient{delegate: delegate, cfg: cfg}
}

func (r *retryingBoskosClient) AcquireWaitWithPriority(ctx context.Context, rtype, state, dest, requestID string) (*common.Resource, error) {
	var res *common.Resource
	err := retryOnServerError(ctx, r.cfg, "AcquireWaitWithPriority", func() error {
		var innerErr error
		res, innerErr = r.delegate.AcquireWaitWithPriority(ctx, rtype, state, dest, requestID)
		return innerErr
	})
	return res, err
}

func (r *retryingBoskosClient) Acquire(rtype, state, dest string) (*common.Resource, error) {
	var res *common.Resource
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.maxTotalTime)
	defer cancel()
	err := retryOnServerError(ctx, r.cfg, "Acquire", func() error {
		var innerErr error
		res, innerErr = r.delegate.Acquire(rtype, state, dest)
		return innerErr
	})
	return res, err
}

func (r *retryingBoskosClient) UpdateOne(name, dest string, userData *common.UserData) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.maxTotalTime)
	defer cancel()
	return retryOnServerError(ctx, r.cfg, "UpdateOne", func() error {
		return r.delegate.UpdateOne(name, dest, userData)
	})
}

func (r *retryingBoskosClient) ReleaseOne(name, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.maxTotalTime)
	defer cancel()
	return retryOnServerError(ctx, r.cfg, "ReleaseOne", func() error {
		return r.delegate.ReleaseOne(name, dest)
	})
}

func (r *retryingBoskosClient) ReleaseAll(dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.maxTotalTime)
	defer cancel()
	return retryOnServerError(ctx, r.cfg, "ReleaseAll", func() error {
		return r.delegate.ReleaseAll(dest)
	})
}

func (r *retryingBoskosClient) Metric(rtype string) (common.Metric, error) {
	var metric common.Metric
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.maxTotalTime)
	defer cancel()
	err := retryOnServerError(ctx, r.cfg, "Metric", func() error {
		var innerErr error
		metric, innerErr = r.delegate.Metric(rtype)
		return innerErr
	})
	return metric, err
}
