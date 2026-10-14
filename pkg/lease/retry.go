package lease

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/sirupsen/logrus"
)

// serverErrorPattern matches HTTP 5xx status codes in error messages produced
// by the upstream Boskos client.  The upstream formats these as either
//
//	"status 502 Bad Gateway, status code 502"          (acquire, metric)
//	"status 502 Bad Gateway, statusCode 502 releasing …" (release)
//	"status 502 Bad Gateway, status code 502 updating …" (update)
var serverErrorPattern = regexp.MustCompile(`(?:statusCode|status code) 5\d{2}`)

// isRetryableServerError reports whether err indicates a 5xx HTTP response
// from the Boskos server.  Only 5xx errors are considered retryable; 4xx
// errors, connection errors, and sentinel errors like ErrNotFound are not.
func isRetryableServerError(err error) bool {
	if err == nil {
		return false
	}
	return serverErrorPattern.MatchString(err.Error())
}

// retryConfig holds parameters for exponential-backoff retry logic.
//
// We use a custom implementation rather than k8s.io/apimachinery/pkg/util/wait
// because our retry loop is bounded by wall-clock time (maxTotalTime) rather
// than a fixed step count (wait.Backoff.Steps), and we need a fake-clock
// interface (nowFunc/sleepFunc) for deterministic unit testing.
type retryConfig struct {
	// initialBackoff is the sleep duration after the first failed attempt.
	initialBackoff time.Duration
	// maxBackoff caps the per-attempt sleep duration.
	maxBackoff time.Duration
	// maxTotalTime is the hard wall-clock limit for the entire retry loop.
	maxTotalTime time.Duration
	// multiplier is applied to the backoff after each failed attempt.
	multiplier float64

	// sleepFunc is called to pause between attempts.  Override for testing.
	sleepFunc func(context.Context, time.Duration) error
	// nowFunc returns the current time.  Override for testing.
	nowFunc func() time.Time
}

// defaultRetryConfig returns a retry configuration targeting ~5 minutes of
// total retry time, which is long enough to ride out transient Boskos
// outages without blocking CI jobs indefinitely.
//
// Backoff sequence: 5 s, 10 s, 20 s, 40 s, 60 s, 60 s, …
func defaultRetryConfig() retryConfig {
	return retryConfig{
		initialBackoff: 5 * time.Second,
		maxBackoff:     60 * time.Second,
		maxTotalTime:   5 * time.Minute,
		multiplier:     2.0,
		sleepFunc:      contextSleep,
		nowFunc:        time.Now,
	}
}

// contextSleep pauses for duration d, returning early if ctx is cancelled.
func contextSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryOnServerError calls fn and, if it returns a 5xx error, retries with
// exponential backoff.  Non-retryable errors and nil are returned immediately.
// The retry loop respects both the provided context and cfg.maxTotalTime.
func retryOnServerError(ctx context.Context, cfg retryConfig, operation string, fn func() error) error {
	// Set the deadline before the first attempt so the total time budget
	// includes the initial call — otherwise a slow first attempt would
	// shift the retry window forward.
	deadline := cfg.nowFunc().Add(cfg.maxTotalTime)

	err := fn()
	if err == nil || !isRetryableServerError(err) {
		return err
	}

	backoff := cfg.initialBackoff

	for attempt := 1; ; attempt++ {
		// If the next sleep would push us past the deadline, give up.
		if cfg.nowFunc().Add(backoff).After(deadline) {
			break
		}

		logrus.WithError(err).Warnf(
			"Boskos server error on %s (attempt %d), retrying in %s",
			operation, attempt, backoff,
		)

		if sleepErr := cfg.sleepFunc(ctx, backoff); sleepErr != nil {
			// Context was cancelled during the sleep — return the last
			// Boskos error so the caller can inspect the root cause.
			return err
		}

		err = fn()
		if err == nil {
			logrus.Infof("Boskos %s succeeded after %d retries", operation, attempt)
			return nil
		}
		if !isRetryableServerError(err) {
			return err
		}

		// Increase backoff for the next iteration, capped at maxBackoff.
		backoff = time.Duration(float64(backoff) * cfg.multiplier)
		if backoff > cfg.maxBackoff {
			backoff = cfg.maxBackoff
		}
	}

	return fmt.Errorf("exhausted retries for %s over %s: %w", operation, cfg.maxTotalTime, err)
}
