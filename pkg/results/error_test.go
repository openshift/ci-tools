package results

import (
	"errors"
	"testing"

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
