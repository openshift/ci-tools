package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// listSecretsMaxAttempts bounds RPC attempts for each discovery page.
	listSecretsMaxAttempts = 3
	// listSecretsDiscoveryTimeout bounds all discovery pages, RPCs and backoff combined.
	listSecretsDiscoveryTimeout = time.Minute
)

// listSecretsRetryer bounds the SDK retry policy for one ListSecrets page.
type listSecretsRetryer struct {
	retryer   gax.Retryer
	remaining int
}

// newListSecretsRetryer creates an independent Unavailable-only policy for each page.
func newListSecretsRetryer() gax.Retryer {
	return &listSecretsRetryer{
		retryer: gax.OnCodes([]codes.Code{codes.Unavailable}, gax.Backoff{
			Initial:    100 * time.Millisecond,
			Max:        time.Second,
			Multiplier: 2,
		}),
		remaining: listSecretsMaxAttempts - 1,
	}
}

// Retry delegates status classification and backoff to GAX within the attempt limit.
func (retryer *listSecretsRetryer) Retry(err error) (time.Duration, bool) {
	if retryer.remaining == 0 {
		return 0, false
	}
	pause, retry := retryer.retryer.Retry(err)
	if retry {
		retryer.remaining--
	}
	return pause, retry
}

// listSecretsDiscoveryError preserves the cause without displaying server response details.
type listSecretsDiscoveryError struct {
	err error
}

// Error reports the discovery operation, status and retry budgets without the RPC message.
func (failure *listSecretsDiscoveryError) Error() string {
	code := status.Code(failure.err)
	if errors.Is(failure.err, context.Canceled) || errors.Is(failure.err, context.DeadlineExceeded) {
		code = status.FromContextError(failure.err).Code()
	}
	return fmt.Sprintf("secretmanager ListSecrets discovery failed (status=%s, max_attempts_per_page=%d, discovery_budget=%s)",
		code, listSecretsMaxAttempts, listSecretsDiscoveryTimeout)
}

// Unwrap exposes the original cause for programmatic error inspection.
func (failure *listSecretsDiscoveryError) Unwrap() error {
	return failure.err
}
