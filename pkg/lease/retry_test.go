package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIsRetryableServerError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("something went wrong"),
			want: false,
		},
		{
			name: "ErrNotFound",
			err:  ErrNotFound,
			want: false,
		},
		{
			name: "ErrTypeNotFound",
			err:  ErrTypeNotFound,
			want: false,
		},
		{
			name: "connection refused",
			err:  errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"),
			want: false,
		},
		{
			name: "HTTP 400",
			err:  errors.New("status 400 Bad Request, status code 400"),
			want: false,
		},
		{
			name: "HTTP 404",
			err:  errors.New("status 404 Not Found, status code 404"),
			want: false,
		},
		{
			name: "HTTP 500 - status code format",
			err:  errors.New("status 500 Internal Server Error, status code 500"),
			want: true,
		},
		{
			name: "HTTP 502 - status code format",
			err:  errors.New("status 502 Bad Gateway, status code 502"),
			want: true,
		},
		{
			name: "HTTP 502 - statusCode format (release)",
			err:  errors.New("status 502 Bad Gateway, statusCode 502 releasing my-lease"),
			want: true,
		},
		{
			name: "HTTP 502 - statusCode format (update)",
			err:  errors.New("status 502 Bad Gateway, status code 502 updating my-lease"),
			want: true,
		},
		{
			name: "HTTP 503",
			err:  errors.New("status 503 Service Unavailable, status code 503"),
			want: true,
		},
		{
			name: "HTTP 599",
			err:  errors.New("status 599 Custom, status code 599"),
			want: true,
		},
		{
			name: "aggregated error with 502",
			err:  errors.New("[status 502 Bad Gateway, status code 502, status 502 Bad Gateway, status code 502]"),
			want: true,
		},
		{
			name: "wrapped error with 502",
			err:  fmt.Errorf("acquire failed: %w", errors.New("status 502 Bad Gateway, status code 502")),
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableServerError(tc.err); got != tc.want {
				t.Errorf("isRetryableServerError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// fakeRetryConfig returns a retryConfig with a fake clock for deterministic testing.
func fakeRetryConfig(maxTotalTime time.Duration) retryConfig {
	fakeNow := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return retryConfig{
		initialBackoff: 5 * time.Second,
		maxBackoff:     30 * time.Second,
		maxTotalTime:   maxTotalTime,
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

func TestRetryOnServerError_ImmediateSuccess(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(time.Minute)
	calls := 0
	err := retryOnServerError(context.Background(), cfg, "test", func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnServerError_NonRetryableError(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(time.Minute)
	nonRetryable := errors.New("status 404 Not Found, status code 404")
	calls := 0
	err := retryOnServerError(context.Background(), cfg, "test", func() error {
		calls++
		return nonRetryable
	})
	if err != nonRetryable {
		t.Fatalf("expected non-retryable error returned directly, got: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry for non-5xx), got %d", calls)
	}
}

func TestRetryOnServerError_RetryThenSucceed(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(time.Minute)
	retryableErr := errors.New("status 502 Bad Gateway, status code 502")
	calls := 0
	err := retryOnServerError(context.Background(), cfg, "test", func() error {
		calls++
		if calls <= 3 {
			return retryableErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	// 1 initial call + 3 retries = 4 total
	if calls != 4 {
		t.Fatalf("expected 4 calls, got %d", calls)
	}
}

func TestRetryOnServerError_ExhaustsRetries(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(time.Minute)
	retryableErr := errors.New("status 502 Bad Gateway, status code 502")
	calls := 0
	err := retryOnServerError(context.Background(), cfg, "test-op", func() error {
		calls++
		return retryableErr
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "exhausted retries for test-op") {
		t.Fatalf("expected 'exhausted retries' message, got: %v", err)
	}
	// Verify the original error is wrapped
	if !errors.Is(err, retryableErr) {
		t.Fatalf("expected original error to be wrapped, got: %v", err)
	}
	// With maxTotalTime=1m, initialBackoff=5s, maxBackoff=30s, multiplier=2.0:
	// sleeps: 5s, 10s, 20s → total 35s, next would be 30s which exceeds 60s
	// So: 1 initial + 3 retries = 4 calls
	if calls != 4 {
		t.Fatalf("expected 4 calls, got %d", calls)
	}
}

func TestRetryOnServerError_RetryThenNonRetryableError(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(time.Minute)
	retryableErr := errors.New("status 502 Bad Gateway, status code 502")
	nonRetryableErr := errors.New("resource not found")
	calls := 0
	err := retryOnServerError(context.Background(), cfg, "test", func() error {
		calls++
		if calls <= 2 {
			return retryableErr
		}
		return nonRetryableErr
	})
	if err != nonRetryableErr {
		t.Fatalf("expected non-retryable error, got: %v", err)
	}
	// 1 initial + 2 retries = 3 calls
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOnServerError_ContextCancelled(t *testing.T) {
	t.Parallel()
	retryableErr := errors.New("status 502 Bad Gateway, status code 502")
	ctx, cancel := context.WithCancel(context.Background())

	fakeNow := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := retryConfig{
		initialBackoff: 5 * time.Second,
		maxBackoff:     30 * time.Second,
		maxTotalTime:   time.Minute,
		multiplier:     2.0,
		sleepFunc: func(ctx context.Context, d time.Duration) error {
			// Simulate context cancellation during the second sleep
			cancel()
			return ctx.Err()
		},
		nowFunc: func() time.Time {
			return fakeNow
		},
	}

	calls := 0
	err := retryOnServerError(ctx, cfg, "test", func() error {
		calls++
		return retryableErr
	})
	// Should return the last boskos error, not a context error
	if err != retryableErr {
		t.Fatalf("expected original boskos error on context cancellation, got: %v", err)
	}
	// 1 initial call + sleep cancelled = 1 call total
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryOnServerError_BackoffProgression(t *testing.T) {
	t.Parallel()
	cfg := fakeRetryConfig(10 * time.Minute) // long deadline so we observe all backoffs
	cfg.initialBackoff = 1 * time.Second
	cfg.maxBackoff = 16 * time.Second
	cfg.multiplier = 2.0

	retryableErr := errors.New("status 502 Bad Gateway, status code 502")
	var sleepDurations []time.Duration
	originalSleep := cfg.sleepFunc
	cfg.sleepFunc = func(ctx context.Context, d time.Duration) error {
		sleepDurations = append(sleepDurations, d)
		return originalSleep(ctx, d)
	}

	calls := 0
	maxCalls := 7
	err := retryOnServerError(context.Background(), cfg, "test", func() error {
		calls++
		if calls >= maxCalls {
			return nil
		}
		return retryableErr
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected backoff sequence: 1s, 2s, 4s, 8s, 16s, 16s (capped)
	expectedSleeps := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		16 * time.Second,
	}
	if len(sleepDurations) != len(expectedSleeps) {
		t.Fatalf("expected %d sleeps, got %d: %v", len(expectedSleeps), len(sleepDurations), sleepDurations)
	}
	for i, want := range expectedSleeps {
		if sleepDurations[i] != want {
			t.Errorf("sleep[%d] = %s, want %s", i, sleepDurations[i], want)
		}
	}
}
