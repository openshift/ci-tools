package util

import (
	"errors"
)

// TerminalError returns a new terminal error which indicates that there is no point in retrying
// reconciliation. The reconciler should use SwallowIfTerminal to wrap any errors it got before
// returning them.
func TerminalError(inner error) error {
	return nonRetriableError{err: inner}
}

// SwallowIfTerminal will swallow errors if they are terminal. It supports wrapped errors.
func SwallowIfTerminal(err error) error {
	if errors.Is(err, nonRetriableError{}) {
		return nil
	}
	return err
}

// IsTerminal indicates if a given error is terminal
func IsTerminal(err error) bool {
	return SwallowIfTerminal(err) == nil
}

// nonRetriableError indicates that we encountered an error
// that we know wont resolve itself via retrying. We use it
// to still bubble the message up but swallow it after we
// logged it so we don't waste cycles on useless work.
type nonRetriableError struct {
	err error
}

// errors.Is compares via == which means if our .err holds something,
// we never match.
func (nonRetriableError) Is(target error) bool {
	_, ok := target.(nonRetriableError)
	return ok
}

func (nre nonRetriableError) Error() string {
	return nre.err.Error()
}
