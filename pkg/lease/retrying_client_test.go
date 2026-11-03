package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/boskos/common"
)

// errorSequenceBoskos is a test double for boskosClient that returns errors
// from a pre-configured sequence, then succeeds.  It tracks the number of
// calls made to each method.
type errorSequenceBoskos struct {
	// errors is the sequence of errors to return on successive calls.
	// Once exhausted, all subsequent calls succeed.
	errors []error
	calls  int
}

func (e *errorSequenceBoskos) nextErr() error {
	idx := e.calls
	e.calls++
	if idx < len(e.errors) {
		return e.errors[idx]
	}
	return nil
}

func (e *errorSequenceBoskos) AcquireWaitWithPriority(_ context.Context, rtype, _, _, _ string) (*common.Resource, error) {
	if err := e.nextErr(); err != nil {
		return nil, err
	}
	return &common.Resource{Name: rtype + "-lease"}, nil
}

func (e *errorSequenceBoskos) Acquire(rtype, _, _ string) (*common.Resource, error) {
	if err := e.nextErr(); err != nil {
		return nil, err
	}
	return &common.Resource{Name: rtype + "-lease"}, nil
}

func (e *errorSequenceBoskos) UpdateOne(_, _ string, _ *common.UserData) error {
	return e.nextErr()
}

func (e *errorSequenceBoskos) ReleaseOne(_, _ string) error {
	return e.nextErr()
}

func (e *errorSequenceBoskos) ReleaseAll(_ string) error {
	return e.nextErr()
}

func (e *errorSequenceBoskos) Metric(rtype string) (common.Metric, error) {
	if err := e.nextErr(); err != nil {
		return common.Metric{}, err
	}
	return common.NewMetric(rtype), nil
}

// server502 returns an error that mimics a Boskos 502 after the upstream
// client's internal retry loop is exhausted.
func server502() error {
	return errors.New("[status 502 Bad Gateway, status code 502, status 502 Bad Gateway, status code 502]")
}

// testRetryConfig returns a retryConfig with a fake clock for fast,
// deterministic testing.
func testRetryConfig() retryConfig {
	fakeNow := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return retryConfig{
		initialBackoff: 5 * time.Second,
		maxBackoff:     30 * time.Second,
		maxTotalTime:   time.Minute,
		multiplier:     2.0,
		sleepFunc: func(_ context.Context, d time.Duration) error {
			fakeNow = fakeNow.Add(d)
			return nil
		},
		nowFunc: func() time.Time {
			return fakeNow
		},
	}
}

func TestRetryingClient_AcquireWaitWithPriority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		errors    []error
		wantCalls int
		wantErr   bool
		wantName  string
	}{
		{
			name:      "succeeds immediately",
			errors:    nil,
			wantCalls: 1,
			wantName:  "rtype-lease",
		},
		{
			name:      "retries on 502 then succeeds",
			errors:    []error{server502(), server502()},
			wantCalls: 3,
			wantName:  "rtype-lease",
		},
		{
			name:      "does not retry on 404",
			errors:    []error{errors.New("resource not found")},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "does not retry on ErrNotFound",
			errors:    []error{ErrNotFound},
			wantCalls: 1,
			wantErr:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &errorSequenceBoskos{errors: tc.errors}
			client := withRetry(fake, testRetryConfig())

			res, err := client.AcquireWaitWithPriority(context.Background(), "rtype", "free", "leased", "req-1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res.Name != tc.wantName {
					t.Errorf("resource name = %q, want %q", res.Name, tc.wantName)
				}
			}
			if fake.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", fake.calls, tc.wantCalls)
			}
		})
	}
}

func TestRetryingClient_Acquire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		errors    []error
		wantCalls int
		wantErr   bool
		wantName  string
	}{
		{
			name:      "succeeds immediately",
			errors:    nil,
			wantCalls: 1,
			wantName:  "rtype-lease",
		},
		{
			name:      "retries on 502 then succeeds",
			errors:    []error{server502()},
			wantCalls: 2,
			wantName:  "rtype-lease",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &errorSequenceBoskos{errors: tc.errors}
			client := withRetry(fake, testRetryConfig())

			res, err := client.Acquire("rtype", "free", "leased")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res.Name != tc.wantName {
					t.Errorf("resource name = %q, want %q", res.Name, tc.wantName)
				}
			}
			if fake.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", fake.calls, tc.wantCalls)
			}
		})
	}
}

func TestRetryingClient_UpdateOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		errors    []error
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "succeeds immediately",
			errors:    nil,
			wantCalls: 1,
		},
		{
			name:      "retries on 502 then succeeds",
			errors:    []error{server502(), server502()},
			wantCalls: 3,
		},
		{
			name:      "does not retry non-5xx",
			errors:    []error{errors.New("no resource name foo")},
			wantCalls: 1,
			wantErr:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &errorSequenceBoskos{errors: tc.errors}
			client := withRetry(fake, testRetryConfig())

			err := client.UpdateOne("my-lease", "leased", nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fake.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", fake.calls, tc.wantCalls)
			}
		})
	}
}

func TestRetryingClient_ReleaseOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		errors    []error
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "succeeds immediately",
			errors:    nil,
			wantCalls: 1,
		},
		{
			name:      "retries on 502 then succeeds",
			errors:    []error{server502()},
			wantCalls: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &errorSequenceBoskos{errors: tc.errors}
			client := withRetry(fake, testRetryConfig())

			err := client.ReleaseOne("my-lease", "free")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fake.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", fake.calls, tc.wantCalls)
			}
		})
	}
}

func TestRetryingClient_ReleaseAll(t *testing.T) {
	t.Parallel()
	fake := &errorSequenceBoskos{errors: []error{server502()}}
	client := withRetry(fake, testRetryConfig())

	err := client.ReleaseAll("free")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.calls != 2 {
		t.Errorf("calls = %d, want 2", fake.calls)
	}
}

func TestRetryingClient_Metric(t *testing.T) {
	t.Parallel()
	fake := &errorSequenceBoskos{errors: []error{server502()}}
	client := withRetry(fake, testRetryConfig())

	metric, err := client.Metric("my-type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metric.Type != "my-type" {
		t.Errorf("metric type = %q, want %q", metric.Type, "my-type")
	}
	if fake.calls != 2 {
		t.Errorf("calls = %d, want 2", fake.calls)
	}
}

func TestRetryingClient_ExhaustsRetriesOnPersistent502(t *testing.T) {
	t.Parallel()
	// Create enough 502 errors to exhaust the retry window.
	errs := make([]error, 20)
	for i := range errs {
		errs[i] = server502()
	}
	fake := &errorSequenceBoskos{errors: errs}
	client := withRetry(fake, testRetryConfig())

	err := client.UpdateOne("my-lease", "leased", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "exhausted retries") {
		t.Fatalf("expected 'exhausted retries' in error message, got: %v", err)
	}
}

// TestRetryingClient_EndToEnd exercises the full stack: retryingBoskosClient
// → lease.Client → Acquire/Heartbeat/Release, verifying that transient 502s
// are retried transparently.
func TestRetryingClient_EndToEnd(t *testing.T) {
	t.Parallel()

	// Build a fake boskos that fails the first Acquire call with a 502,
	// then succeeds.  Updates and releases succeed immediately.
	acquireCalls := 0
	fake := &programmableBoskos{
		acquireWaitFn: func(_ context.Context, rtype, _, _, _ string) (*common.Resource, error) {
			acquireCalls++
			if acquireCalls == 1 {
				return nil, server502()
			}
			return &common.Resource{Name: fmt.Sprintf("%s_0", rtype)}, nil
		},
		updateOneFn:  func(_, _ string, _ *common.UserData) error { return nil },
		releaseOneFn: func(_, _ string) error { return nil },
		releaseAllFn: func(_ string) error { return nil },
		metricFn:     func(rtype string) (common.Metric, error) { return common.NewMetric(rtype), nil },
	}

	// Use the full client stack: boskosDirect=fake, boskos=withRetry(fake).
	// Heartbeat and AcquireIfAvailableImmediately use the direct client;
	// Acquire and Release use the retry-wrapped client.
	c := newClient(
		fake,
		withRetry(fake, testRetryConfig()),
		/* retries= */ 2,
		/* acquireTimeout= */ time.Minute,
		WithRandID(func() string { return "random" }),
	)

	// Acquire should succeed despite the first 502.
	ctx := context.Background()
	names, err := c.Acquire("aws", 1, ctx, func() {})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if len(names) != 1 || names[0] != "aws_0" {
		t.Fatalf("unexpected lease names: %v", names)
	}
	if acquireCalls != 2 {
		t.Fatalf("expected 2 acquire calls (1 fail + 1 success), got %d", acquireCalls)
	}

	// Heartbeat should work.
	if err := c.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	// Release should work.
	if err := c.Release("aws_0"); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
}

// programmableBoskos is a flexible test double that allows per-method
// function injection.
type programmableBoskos struct {
	acquireWaitFn func(ctx context.Context, rtype, state, dest, requestID string) (*common.Resource, error)
	acquireFn     func(rtype, state, dest string) (*common.Resource, error)
	updateOneFn   func(name, dest string, userData *common.UserData) error
	releaseOneFn  func(name, dest string) error
	releaseAllFn  func(dest string) error
	metricFn      func(rtype string) (common.Metric, error)
}

func (p *programmableBoskos) AcquireWaitWithPriority(ctx context.Context, rtype, state, dest, requestID string) (*common.Resource, error) {
	if p.acquireWaitFn != nil {
		return p.acquireWaitFn(ctx, rtype, state, dest, requestID)
	}
	return &common.Resource{Name: rtype + "_0"}, nil
}

func (p *programmableBoskos) Acquire(rtype, state, dest string) (*common.Resource, error) {
	if p.acquireFn != nil {
		return p.acquireFn(rtype, state, dest)
	}
	return &common.Resource{Name: rtype + "_0"}, nil
}

func (p *programmableBoskos) UpdateOne(name, dest string, userData *common.UserData) error {
	if p.updateOneFn != nil {
		return p.updateOneFn(name, dest, userData)
	}
	return nil
}

func (p *programmableBoskos) ReleaseOne(name, dest string) error {
	if p.releaseOneFn != nil {
		return p.releaseOneFn(name, dest)
	}
	return nil
}

func (p *programmableBoskos) ReleaseAll(dest string) error {
	if p.releaseAllFn != nil {
		return p.releaseAllFn(dest)
	}
	return nil
}

func (p *programmableBoskos) Metric(rtype string) (common.Metric, error) {
	if p.metricFn != nil {
		return p.metricFn(rtype)
	}
	return common.NewMetric(rtype), nil
}
