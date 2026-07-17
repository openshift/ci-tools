package gsmvalidation

import "testing"

func TestDecodeMountFileName(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name:     "simple name",
			input:    "api-token",
			expected: "api-token",
		},
		{
			name:     "encoded dot prefix",
			input:    "--dot--dockerconfigjson",
			expected: ".dockerconfigjson",
		},
		{
			name:     "encoded dots in middle",
			input:    "sa--dot--ci-operator--dot--token--dot--txt",
			expected: "sa.ci-operator.token.txt",
		},
		{
			name:     "GSM encoded field name decodes to dot-prefixed mount file",
			input:    "--dot--awscred",
			expected: ".awscred",
		},
		{
			name:     "as alias with literal dots passes through unchanged",
			input:    ".awscred",
			expected: ".awscred",
		},
		{
			name:     "consecutive dots in filename are not path traversal",
			input:    "version..json",
			expected: "version..json",
		},
		{
			name:        "absolute path after decode",
			input:       "/etc/passwd",
			expectError: true,
		},
		{
			name:        "parent directory segment",
			input:       "foo/../bar",
			expectError: true,
		},
		{
			name:        "subdirectory in mount filename",
			input:       "dir/file",
			expectError: true,
		},
		{
			name:        "parent directory filename",
			input:       "..",
			expectError: true,
		},
		{
			name:        "forbidden characters",
			input:       "bad$name",
			expectError: true,
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := DecodeMountFileName(tc.input)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for %q, got decoded %q", tc.input, actual)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tc.expected {
				t.Fatalf("expected decoded name %q, got %q", tc.expected, actual)
			}
		})
	}
}
