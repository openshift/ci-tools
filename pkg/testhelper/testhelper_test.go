package testhelper

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "simple, no changes",
			in:       "my_golden.yaml",
			expected: "zz_fixture_my_golden.yaml",
		},
		{
			name:     "complex",
			in:       "my_Go\\l'de`n.yaml",
			expected: "zz_fixture_my_Go_l_de_n.yaml",
		},
		{
			name:     "no double underscores",
			in:       "a_|",
			expected: "zz_fixture_a_",
		},
		{
			name:     "numbers are kept",
			in:       "0123456789.yaml",
			expected: "zz_fixture_0123456789.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if result := sanitizeFilename(tc.in); result != tc.expected {
				t.Errorf("expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestEquateErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		x        error
		y        error
		expected bool
	}{
		{
			name: "x is nil",
			y:    fmt.Errorf("error y"),
		},
		{
			name: "y is nil",
			x:    fmt.Errorf("error x"),
		},
		{
			name:     "both are nil",
			expected: true,
		},
		{
			name:     "neither are nil",
			x:        fmt.Errorf("same error"),
			y:        fmt.Errorf("same error"),
			expected: true,
		},
		{
			name:     "wrap error: false",
			x:        fmt.Errorf("wrap error: same error"),
			y:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			expected: false,
		},
		{
			name:     "wrap error: true",
			x:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			y:        fmt.Errorf("wrap error: %w", fmt.Errorf("same error")),
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualDiffString := cmp.Diff(tc.x, tc.y, EquateErrorMessage)
			actual := actualDiffString == ""
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", actualDiffString)
			}
		})
	}
}
