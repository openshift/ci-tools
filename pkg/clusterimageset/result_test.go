package clusterimageset

import (
	"errors"
	"testing"
)

func TestLookupFailuresError(t *testing.T) {
	for _, message := range []string{"lookup failed", "multiple lookups failed"} {
		t.Run(message, func(t *testing.T) {
			err := &LookupFailures{Err: errors.New(message)}
			if err.Error() != message {
				t.Errorf("want %q, got %q", message, err.Error())
			}
		})
	}
}

func TestLookupFailuresUnwrap(t *testing.T) {
	for _, message := range []string{"lookup failed", "multiple lookups failed"} {
		t.Run(message, func(t *testing.T) {
			cause := errors.New(message)
			if !errors.Is(&LookupFailures{Err: cause}, cause) {
				t.Error("underlying error lost")
			}
		})
	}
}
