package helpdesk

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFormatItemField(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trim and remove beginning formatting",
			input:    " &gt; This is a question...any tips? ",
			expected: "This is a question...any tips?",
		},
		{
			name:     "multi-line question",
			input:    " &gt; This is a question\n&gt;...any tips? ",
			expected: "This is a question\n...any tips?",
		},
		{
			name:     "slack link formatting removed",
			input:    " &gt; This is a question containing a link: <https://github.com/openshift/release/pull/1234> ",
			expected: "This is a question containing a link: https://github.com/openshift/release/pull/1234",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatItemField(tc.input)
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}
