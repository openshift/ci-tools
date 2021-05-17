package results

import (
	"errors"
	"fmt"
	"testing"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestError(t *testing.T) {
	base := errors.New("failure")
	testhelper.Diff(t, "reason for base error", FullReason(base), "unknown")
	initial := ForReason("oops").WithError(base).Errorf("couldn't do it")
	testhelper.Diff(t, "reason for initial error", FullReason(initial), "oops")
	second := ForReason("whoopsie").WithError(initial).Errorf("couldn't do it")
	testhelper.Diff(t, "reason for second error", FullReason(second), "whoopsie:oops")
	third := ForReason("argh").WithError(second).Errorf("couldn't do it")
	testhelper.Diff(t, "reason for third error", FullReason(third), "argh:whoopsie:oops")
	simple := ForReason("simple").ForError(base)
	testhelper.Diff(t, "reason for simple error", FullReason(simple), "simple")

	none := ForReason("fake").ForError(nil)
	if none != nil {
		t.Errorf("expected a wrapped nil error to be nil, got %v", none)
	}

	alsoNone := DefaultReason(nil)
	if alsoNone != nil {
		t.Errorf("expected a wrapped nil error to be nil, got %v", alsoNone)
	}
	withDefault := DefaultReason(base)
	testhelper.Diff(t, "reason for defaulted error", FullReason(withDefault), "unknown")
	unchanged := DefaultReason(initial)
	testhelper.Diff(t, "reason for unchanged error", FullReason(unchanged), "oops")
}

func TestComplexError(t *testing.T) {
	work := func() error {
		return errors.New("root error")
	}

	do := func() error {
		if err := work(); err != nil {
			return ForReason("root_thing").WithError(err).Errorf("failed to do root thing: %v", err)
		}
		return nil
	}

	run := func() error {
		return ForReason("higher_level_thing").ForError(do())
	}

	err := run()
	testhelper.Diff(t, "reason for top-level error", FullReason(err), "higher_level_thing:root_thing")
}

func TestReasons(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		expected []string
	}{{
		name: "regular error",
		err:  errors.New("regular"),
	}, {
		name: "single reason",
		err: &Error{
			reason:  "reason",
			message: "msg",
			wrapped: errors.New("error"),
		},
		expected: []string{"reason"},
	}, {
		name: "child reason",
		err: fmt.Errorf("wrapped: %w", &Error{
			reason:  "reason",
			message: "msg",
			wrapped: errors.New("error"),
		}),
		expected: []string{"reason"},
	}, {
		name: "reason aggregate",
		err: utilerrors.NewAggregate([]error{
			&Error{reason: "reason0", message: "msg0"},
			&Error{reason: "reason1", message: "msg1"},
			&Error{reason: "reason2", message: "msg2"},
		}),
		expected: []string{"reason0", "reason1", "reason2"},
	}, {
		name: "reason with aggregate",
		err: &Error{
			reason:  "reason",
			message: "msg",
			wrapped: utilerrors.NewAggregate([]error{
				errors.New("aggregate0"),
				errors.New("aggregate1"),
				errors.New("aggregate2"),
			}),
		},
		expected: []string{"reason"},
	}, {
		name: "reasons with intermediate errors",
		err: &Error{
			reason:  "top_reason",
			message: "top msg",
			wrapped: fmt.Errorf(
				"intermediate0: %w",
				fmt.Errorf(
					"intermediate1: %w",
					&Error{
						reason:  "bottom_reason",
						message: "bottom msg",
						wrapped: errors.New("bottom err"),
					},
				),
			),
		},
		expected: []string{"top_reason:bottom_reason"},
	}, {
		name: "error tree",
		err: fmt.Errorf("root: %w", &Error{
			reason:  "top_reason",
			message: "top msg",
			wrapped: utilerrors.NewAggregate([]error{
				errors.New("regular0"),
				utilerrors.NewAggregate([]error{
					errors.New("regular1"),
					errors.New("regular2"),
				}),
				&Error{
					reason:  "middle_reason0",
					message: "middle msg0",
					wrapped: utilerrors.NewAggregate([]error{
						&Error{
							reason:  "bottom_reason0",
							message: "bottom msg0",
							wrapped: errors.New("bottom err0"),
						},
						&Error{
							reason:  "bottom_reason1",
							message: "bottom msg0",
							wrapped: errors.New("bottom err1"),
						},
					}),
				},
				&Error{
					reason:  "middle_reason1",
					message: "middle msg1",
					wrapped: utilerrors.NewAggregate([]error{
						&Error{
							reason:  "bottom_reason2",
							message: "bottom msg2",
							wrapped: errors.New("bottom err2"),
						},
					}),
				},
			}),
		}),
		expected: []string{
			"top_reason:middle_reason0:bottom_reason0",
			"top_reason:middle_reason0:bottom_reason1",
			"top_reason:middle_reason1:bottom_reason2",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			testhelper.Diff(t, "reasons", Reasons(tc.err), tc.expected)
		})
	}
}
