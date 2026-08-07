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
		{
			name:     "Cross-major boundary: bump 4.22 to 4.23",
			line:     "release-4.22",
			major:    4,
			expected: "release-4.23",
		},
		{
			name:     "Major 5 dot format",
			line:     "ocp-5.0-testing",
			major:    5,
			expected: "ocp-5.1-testing",
		},
		{
			name:     "Dot format substring false positive: 15.0 not bumped when major is 5",
			line:     "product-15.0",
			major:    5,
			expected: "product-15.0",
		},
		{
			name:     "Dot format: only standalone version bumped, not substring",
			line:     "thing-5.0 thing-15.0",
			major:    5,
			expected: "thing-5.1 thing-15.0",
		},
		{
			name:     "Adjacent dot versions both bumped",
			line:     "release-5.0-to-5.1",
			major:    5,
			expected: "release-5.1-to-5.2",
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

func TestReplaceVersionVariants(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		major    int
		expected string
	}{
		// Dot format (same as ReplaceWithNextVersion)
		{
			name:     "Dot format: standard version bump",
			line:     "release-4.10",
			major:    4,
			expected: "release-4.11",
		},
		// Hyphen format
		{
			name:     "Hyphen format: Slack channel style",
			line:     "cnv-release-5-0-z",
			major:    5,
			expected: "cnv-release-5-1-z",
		},
		{
			name:     "Hyphen format: component readiness",
			line:     "prow-ocp-5-0-component-readiness",
			major:    5,
			expected: "prow-ocp-5-1-component-readiness",
		},
		{
			name:     "Hyphen format: major mismatch ignored",
			line:     "thing-5-0-suffix",
			major:    4,
			expected: "thing-5-0-suffix",
		},
		// Underscore format
		{
			name:     "Underscore format: template name",
			line:     "reporter_template_5_0",
			major:    5,
			expected: "reporter_template_5_1",
		},
		{
			name:     "Underscore format: major mismatch ignored",
			line:     "template_5_0",
			major:    4,
			expected: "template_5_0",
		},
		// Mixed formats in one string
		{
			name:     "Mixed: dot and hyphen in same string",
			line:     "job-5.0-and-channel-5-0-foo",
			major:    5,
			expected: "job-5.1-and-channel-5-1-foo",
		},
		{
			name:     "Mixed: all three formats",
			line:     "v5.0 name-5-0-x tmpl_5_0",
			major:    5,
			expected: "v5.1 name-5-1-x tmpl_5_1",
		},
		// Real-world env var values
		{
			name:     "AGENT_ISO style value",
			line:     "agent-ove-5.0.x86_64.iso",
			major:    5,
			expected: "agent-ove-5.1.x86_64.iso",
		},
		{
			name:     "TELEMETRY_GROUP style value",
			line:     "prow-ocp-5.0-component-readiness",
			major:    5,
			expected: "prow-ocp-5.1-component-readiness",
		},
		{
			name:     "JOB_NAME with version",
			line:     "periodic-ci-openshift-release-master-nightly-5.0-e2e-aws",
			major:    5,
			expected: "periodic-ci-openshift-release-master-nightly-5.1-e2e-aws",
		},
		{
			name:     "REPORTER_TEMPLATE_NAME with underscore version",
			line:     "component_readiness_5_0",
			major:    5,
			expected: "component_readiness_5_1",
		},
		// No match at all
		{
			name:     "No version present",
			line:     "no-version-here",
			major:    5,
			expected: "no-version-here",
		},
		// Edge case: version at start and end
		{
			name:     "Version at start of string (hyphen)",
			line:     "5-0-suffix",
			major:    5,
			expected: "5-1-suffix",
		},
		{
			name:     "Version at end of string (underscore)",
			line:     "prefix_5_0",
			major:    5,
			expected: "prefix_5_1",
		},
		// Substring false-positive guards
		{
			name:     "Substring false positive: only standalone hyphen version is bumped",
			line:     "valid-5-0 invalid-15-0",
			major:    5,
			expected: "valid-5-1 invalid-15-0",
		},
		{
			name:     "Substring false positive: dot format 15.0 not bumped when major is 5",
			line:     "thing-15.0-and-5.0",
			major:    5,
			expected: "thing-15.0-and-5.1",
		},
		{
			name:     "Substring false positive: underscore format 15_0 not bumped",
			line:     "valid_5_0 invalid_15_0",
			major:    5,
			expected: "valid_5_1 invalid_15_0",
		},
		// Adjacent version tokens
		{
			name:     "Adjacent hyphen tokens both bumped",
			line:     "5-0-5-1",
			major:    5,
			expected: "5-1-5-2",
		},
		{
			name:     "Adjacent underscore tokens both bumped",
			line:     "5_0_5_1",
			major:    5,
			expected: "5_1_5_2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := bumper.ReplaceVersionVariants(test.line, test.major)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if result != test.expected {
				t.Errorf("Expected %q but got %q", test.expected, result)
			}
		})
	}
}
