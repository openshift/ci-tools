package clusterimageset

// PartialUpdateExitCode reports lookup failures after all independent updates
// have completed successfully. Other failures must not use this exit code.
const PartialUpdateExitCode = 10

// LookupFailures reports release lookups that failed without changing the
// affected pools or their required image sets.
type LookupFailures struct {
	Err error
}

// Error returns the lookup diagnostics or a fallback for a nil receiver or cause.
func (failures *LookupFailures) Error() string {
	if failures == nil || failures.Err == nil {
		return "release lookup failure without diagnostics"
	}
	return failures.Err.Error()
}

// Unwrap returns the underlying lookup errors, or nil for a nil receiver.
func (failures *LookupFailures) Unwrap() error {
	if failures == nil {
		return nil
	}
	return failures.Err
}
