package dispatcher

import (
	"testing"
)

func TestRemoveRehearsePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "rehearse-53656-periodic-ci-openshift-tests-release-4.17",
			expected: "periodic-ci-openshift-tests-release-4.17",
		},
		{
			input:    "periodic-ci-openshift-tests-release-4.17",
			expected: "periodic-ci-openshift-tests-release-4.17",
		},
		{
			input:    "periodic-ci-openshift-rehearse-88888-tests-release-4.17",
			expected: "periodic-ci-openshift-rehearse-88888-tests-release-4.17",
		},
		{
			input:    "pull-ci-something-else-rehearse-34533",
			expected: "pull-ci-something-else-rehearse-34533",
		},
	}

	for _, tt := range tests {
		result := removeRehearsePrefix(tt.input)

		if result != tt.expected {
			t.Errorf("removeRehearsePrefix(%s) = %s; expected %s", tt.input, result, tt.expected)
		}
	}
}
