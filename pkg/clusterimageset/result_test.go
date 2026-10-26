package clusterimageset

import (
	"errors"
	"testing"
)

func TestLookupFailuresError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *LookupFailures
		want string
	}{
		{name: "nil receiver", want: "release lookup failure without diagnostics"},
		{name: "empty result", err: &LookupFailures{}, want: "release lookup failure without diagnostics"},
		{name: "lookup diagnostics", err: &LookupFailures{Err: errors.New("lookup failed")}, want: "lookup failed"},
		{name: "aggregate diagnostics", err: &LookupFailures{Err: errors.New("multiple lookups failed")}, want: "multiple lookups failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLookupFailuresUnwrap(t *testing.T) {
	cause := errors.New("lookup failed")
	for _, tc := range []struct {
		name string
		err  *LookupFailures
		want error
	}{
		{name: "nil receiver"},
		{name: "empty result", err: &LookupFailures{}},
		{name: "underlying error", err: &LookupFailures{Err: cause}, want: cause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Unwrap(tc.err); got != tc.want {
				t.Errorf("want underlying error %v, got %v", tc.want, got)
			}
		})
	}
}
