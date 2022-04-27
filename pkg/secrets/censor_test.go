package secrets

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDynamicCensor(t *testing.T) {
	censor := NewDynamicCensor()
	input := []byte("secret terces c2VjcmV0 dGVyY2Vz")
	censored := input
	censor.Censor(&censored)
	if diff := cmp.Diff(censored, []byte("secret terces c2VjcmV0 dGVyY2Vz")); diff != "" {
		t.Errorf("unexpected result: %s", diff)
	}
	censored = input
	censor.AddSecrets("secret")
	censor.Censor(&censored)
	if diff := cmp.Diff(censored, []byte("XXXXXX terces XXXXXXXX dGVyY2Vz")); diff != "" {
		t.Errorf("unexpected result: %s", diff)
	}
	censored = input
	censor.AddSecrets("terces")
	censor.Censor(&censored)
	if diff := cmp.Diff(censored, []byte("XXXXXX XXXXXX XXXXXXXX XXXXXXXX")); diff != "" {
		t.Errorf("unexpected result: %s", diff)
	}
}
