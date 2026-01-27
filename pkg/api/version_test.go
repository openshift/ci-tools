package api

import (
	"testing"
)

func TestGetPreviousVersion(t *testing.T) {
	// Sample available versions for testing
	availableVersions := []string{"4.18", "4.19", "4.20", "4.21", "4.22", "5.0", "5.1", "5.2"}

	testCases := []struct {
		name              string
		version           string
		availableVersions []string
		expected          string
		expectError       bool
	}{
		{
			name:              "5.0 uses override to 4.22",
			version:           "5.0",
			availableVersions: availableVersions,
			expected:          "4.22",
		},
		{
			name:              "5.1 computes previous as 5.0 (natural progression)",
			version:           "5.1",
			availableVersions: availableVersions,
			expected:          "5.0",
		},
		{
			name:              "5.2 computes previous as 5.1 (natural progression)",
			version:           "5.2",
			availableVersions: availableVersions,
			expected:          "5.1",
		},
		{
			name:              "4.23 computes previous as 4.22 (natural progression)",
			version:           "4.23",
			availableVersions: availableVersions,
			expected:          "4.22",
		},
		{
			name:              "4.1 computes previous as 4.0 (natural progression)",
			version:           "4.1",
			availableVersions: availableVersions,
			expected:          "4.0",
		},
		{
			name:              "6.0 finds highest 5.x from available (no override)",
			version:           "6.0",
			availableVersions: availableVersions,
			expected:          "5.2", // highest 5.x in availableVersions
		},
		{
			name:              "6.0 with different available versions",
			version:           "6.0",
			availableVersions: []string{"5.0", "5.1", "5.5", "4.22"},
			expected:          "5.5", // highest 5.x
		},
		{
			name:              "4.0 finds highest 3.x from available",
			version:           "4.0",
			availableVersions: []string{"3.10", "3.11", "3.9"},
			expected:          "3.11", // highest 3.x
		},
		{
			name:              "4.0 with no 3.x available",
			version:           "4.0",
			availableVersions: []string{"4.1", "4.2"},
			expectError:       true, // no 3.x versions available
		},
		{
			name:              "invalid version format",
			version:           "invalid",
			availableVersions: availableVersions,
			expectError:       true,
		},
		{
			name:              "version with three parts",
			version:           "4.22.1",
			availableVersions: availableVersions,
			expectError:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := GetPreviousVersion(tc.version, tc.availableVersions)
			if tc.expectError {
				if err == nil {
					t.Errorf("expected error for version %s, got result %s", tc.version, result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for version %s: %v", tc.version, err)
				}
				if result != tc.expected {
					t.Errorf("GetPreviousVersion(%s) = %s, expected %s", tc.version, result, tc.expected)
				}
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	testCases := []struct {
		input       string
		expected    ParsedVersion
		expectError bool
	}{
		{"4.22", ParsedVersion{Major: 4, Minor: 22}, false},
		{"5.0", ParsedVersion{Major: 5, Minor: 0}, false},
		{"10.15", ParsedVersion{Major: 10, Minor: 15}, false},
		{"invalid", ParsedVersion{}, true},
		{"4.22.1", ParsedVersion{}, true},
		{"4", ParsedVersion{}, true},
		{"", ParsedVersion{}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := ParseVersion(tc.input)
			if tc.expectError {
				if err == nil {
					t.Errorf("expected error for %s", tc.input)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for %s: %v", tc.input, err)
				}
				if result != tc.expected {
					t.Errorf("ParseVersion(%s) = %v, expected %v", tc.input, result, tc.expected)
				}
			}
		})
	}
}

func TestGetPreviousVersionSimple(t *testing.T) {
	testCases := []struct {
		name        string
		version     string
		expected    string
		expectError bool
	}{
		{
			name:     "5.0 uses override to 4.22",
			version:  "5.0",
			expected: "4.22",
		},
		{
			name:     "5.1 computes previous as 5.0 (natural progression)",
			version:  "5.1",
			expected: "5.0",
		},
		{
			name:     "5.2 computes previous as 5.1 (natural progression)",
			version:  "5.2",
			expected: "5.1",
		},
		{
			name:     "4.22 computes previous as 4.21 (natural progression)",
			version:  "4.22",
			expected: "4.21",
		},
		{
			name:     "4.1 computes previous as 4.0 (natural progression)",
			version:  "4.1",
			expected: "4.0",
		},
		{
			name:        "6.0 fails without override (cannot determine previous major's last minor)",
			version:     "6.0",
			expectError: true,
		},
		{
			name:        "invalid version format",
			version:     "invalid",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := GetPreviousVersionSimple(tc.version)
			if tc.expectError {
				if err == nil {
					t.Errorf("expected error for version %s, got result %s", tc.version, result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for version %s: %v", tc.version, err)
				}
				if result != tc.expected {
					t.Errorf("GetPreviousVersionSimple(%s) = %s, expected %s", tc.version, result, tc.expected)
				}
			}
		})
	}
}
