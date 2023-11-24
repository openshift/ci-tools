package bumper_test

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/branchcuts/bumper"
)

func TestReplaceWithNextVersion(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		major    int
		expected string
	}{
		{
			name:     "Bump to the next version properly",
			line:     "product_3.2",
			major:    3,
			expected: "product_3.3",
		},
		{
			name:     "Bump skipped due to major mismatch",
			line:     "product_3.2",
			major:    2,
			expected: "product_3.2",
		},
		{
			name:     "Unable to bump when leading zeroes",
			line:     "product_3.002",
			major:    3,
			expected: "product_3.002",
		},
		{
			name:     "Multiple bumping",
			line:     "product_3.2 product_3.9",
			major:    3,
			expected: "product_3.3 product_3.10",
		},
		{
			name:     "Multiple bumping 2",
			line:     "openshift-upgrade-ovirt-release-4.5-4.6",
			major:    4,
			expected: "openshift-upgrade-ovirt-release-4.6-4.7",
		},
		{
			name:     "Multiple bumping with a major mismatch",
			line:     "product_3.2 product_3.9 product_4.1",
			major:    3,
			expected: "product_3.3 product_3.10 product_4.1",
		},
		{
			name:     "Unexpected dot",
			line:     "product_3..2",
			major:    3,
			expected: "product_3..2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			l, err := bumper.ReplaceWithNextVersion(test.line, test.major)
			if err != nil {
				t.Error(err)
			} else if l != test.expected {
				t.Errorf("Expected %s but got %s", test.expected, l)
			}
		})
	}
}
