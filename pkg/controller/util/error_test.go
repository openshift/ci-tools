package util

import (
	"errors"
	"testing"
)

func TestSwallowIfTerminal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		err              error
		expectSwallowing bool
	}{
		{
			name:             "Terminal error gets swallowed",
			err:              TerminalError(errors.New("Goodbye sweet world")),
			expectSwallowing: true,
		},
		{
			name:             "Non-Terminal error persists",
			err:              errors.New("here to stay"),
			expectSwallowing: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if afterSwallowing := SwallowIfTerminal(tc.err); (afterSwallowing == nil) != tc.expectSwallowing {
				t.Errorf("Expected swallowing: %t, got swallowing: %t", tc.expectSwallowing, (afterSwallowing == nil))
			}

			if consideredTerminal := IsTerminal(tc.err); consideredTerminal != tc.expectSwallowing {
				t.Errorf("expected err to be consired terminal: %t, was consired terminal: %t", tc.expectSwallowing, consideredTerminal)
			}
		})
	}
}
