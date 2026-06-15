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

	// Check that response has the expected structure
	if response["response_type"] != "in_channel" {
		t.Errorf("Expected response_type to be 'in_channel', got %v", response["response_type"])
	}

	blocks, ok := response["blocks"].([]map[string]interface{})
	if !ok {
		t.Fatal("Expected blocks to be a slice of maps")
	}

	if len(blocks) != 3 {
		t.Errorf("Expected 3 blocks, got %d", len(blocks))
	}

	// Check header block
	if blocks[0]["type"] != "header" {
		t.Errorf("Expected first block to be header, got %v", blocks[0]["type"])
	}

	// Check section block contains analysis
	if blocks[1]["type"] != "section" {
		t.Errorf("Expected second block to be section, got %v", blocks[1]["type"])
	}

	// Check context block exists
	if blocks[2]["type"] != "context" {
		t.Errorf("Expected third block to be context, got %v", blocks[2]["type"])
	}
}
