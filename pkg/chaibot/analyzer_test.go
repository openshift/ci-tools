package chaibot

import (
	"testing"
	"time"
)

func TestExtractProwURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard prow URL",
			input:    "Job failed: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-stolostron-policy-collection-main-ocp4.22-interop-opp-aws/2066255424226594816",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-stolostron-policy-collection-main-ocp4.22-interop-opp-aws/2066255424226594816",
		},
		{
			name:     "prow PR URL",
			input:    "Check this: https://prow.ci.openshift.org/?pr=12345 please",
			expected: "https://prow.ci.openshift.org/?pr=12345",
		},
		{
			name:     "deck internal URL",
			input:    "https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/view/gcs/test-platform-results/logs/job/123",
			expected: "https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/view/gcs/test-platform-results/logs/job/123",
		},
		{
			name:     "no URL",
			input:    "This is just a normal message",
			expected: "",
		},
		{
			name:     "URL with trailing text",
			input:    "Failed again https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/job/456 any ideas?",
			expected: "https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/job/456",
		},
		{
			name:     "URL with trailing parenthesis",
			input:    "Check this out (https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/789)",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/789",
		},
		{
			name:     "URL with trailing angle bracket",
			input:    "<https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/999>",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/999",
		},
		{
			name:     "URL with trailing period",
			input:    "Failed job: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/111.",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/111",
		},
		{
			name:     "URL with trailing comma",
			input:    "Jobs failed: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/222, and more",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/222",
		},
		{
			name:     "URL with multiple trailing punctuation",
			input:    "See: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/333>.",
			expected: "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/333",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractProwURL(tt.input)
			if result != tt.expected {
				t.Errorf("ExtractProwURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestContainsProwURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "contains prow URL",
			input:    "Job failed: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/123",
			expected: true,
		},
		{
			name:     "no prow URL",
			input:    "This is just a message",
			expected: false,
		},
		{
			name:     "contains other URL",
			input:    "Check out https://github.com/openshift/release",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsProwURL(tt.input)
			if result != tt.expected {
				t.Errorf("ContainsProwURL(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatSlackResponse(t *testing.T) {
	result := &AnalysisResult{
		JobURL:   "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/123",
		Analysis: "Test analysis result",
		Duration: 42 * time.Second,
	}

	response := FormatSlackResponse(result)

	// Check that response is a string
	if response == "" {
		t.Error("Expected non-empty response string")
	}

	// Check that response contains the analysis text
	if !containsString(response, "Test analysis result") {
		t.Errorf("Expected response to contain analysis text, got: %s", response)
	}

	// Check that response contains the duration
	if !containsString(response, "42.0s") {
		t.Errorf("Expected response to contain duration, got: %s", response)
	}

	// Check that response contains "Chaibot" or similar branding
	if !containsString(response, "Chaibot") && !containsString(response, "Chai Bot") {
		t.Errorf("Expected response to contain branding, got: %s", response)
	}
}

func TestFormatSlackResponse_NilResult(t *testing.T) {
	// Should not panic with nil result
	response := FormatSlackResponse(nil)

	// Check that response has error message
	if response == "" {
		t.Error("Expected non-empty error message")
	}

	// Check that response contains error indicator
	if !containsString(response, "Error") && !containsString(response, "❌") {
		t.Errorf("Expected error response, got: %s", response)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
