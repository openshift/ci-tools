package results

import (
	"errors"
	"testing"
)

func TestError(t *testing.T) {
	base := errors.New("failure")
	if actual, expected := FullReason(base), "unknown"; actual != expected {
		t.Errorf("got incorrect reason for base error; expected %s, got %v", expected, actual)
	}
	initial := ForReason("oops").WithError(base).Errorf("couldn't do it")
	if actual, expected := FullReason(initial), "oops"; actual != expected {
		t.Errorf("got incorrect reason for initial error; expected %s, got %v", expected, actual)
	}
	second := ForReason("whoopsie").WithError(initial).Errorf("couldn't do it")
	if actual, expected := FullReason(second), "whoopsie:oops"; actual != expected {
		t.Errorf("got incorrect reason for second error; expected %s, got %v", expected, actual)
	}
	third := ForReason("argh").WithError(second).Errorf("couldn't do it")
	if actual, expected := FullReason(third), "argh:whoopsie:oops"; actual != expected {
		t.Errorf("got incorrect reason for third error; expected %s, got %v", expected, actual)
	}

	simple := ForReason("simple").ForError(base)
	if actual, expected := FullReason(simple), "simple"; actual != expected {
		t.Errorf("got incorrect reason for simple error; expected %s, got %v", expected, actual)
	}

	none := ForReason("fake").ForError(nil)
	if none != nil {
		t.Errorf("expected a wrapped nil error to be nil, got %v", none)
	}

	alsoNone := DefaultReason(nil)
	if alsoNone != nil {
		t.Errorf("expected a wrapped nil error to be nil, got %v", alsoNone)
	}
	withDefault := DefaultReason(base)
	if actual, expected := FullReason(withDefault), "unknown"; actual != expected {
		t.Errorf("got incorrect reason for defaulted error; expected %s, got %v", expected, actual)
	}
	unchanged := DefaultReason(initial)
	if actual, expected := FullReason(unchanged), "oops"; actual != expected {
		t.Errorf("got incorrect reason for unchanged error; expected %s, got %v", expected, actual)
	}
}
