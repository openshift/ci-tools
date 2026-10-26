package clusterimageset

// PartialUpdateExitCode reports lookup failures after all independent updates
// have completed successfully. Other failures must not use this exit code.
const PartialUpdateExitCode = 10

// LookupFailures reports release lookups that failed without changing the
// affected pools or their required image sets.
type LookupFailures struct {
	Err error
}

// Error returns the lookup diagnostics.
func (failures *LookupFailures) Error() string {
	return failures.Err.Error()
}

// Unwrap returns the underlying lookup errors.
func (failures *LookupFailures) Unwrap() error {
	return failures.Err
}
