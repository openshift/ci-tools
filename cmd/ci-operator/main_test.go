package main

import "testing"

func TestSanitizeMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{{
		name:     "pod name",
		message:  "...pod ci-op-4fg72pn0/unit...",
		expected: "...pod <PODNAME>/unit...",
	}, {
		name:     "ci-operator duration seconds",
		message:  "...after 39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds-like pattern not replaced inside words",
		message:  "some hash is 'h4sh'",
		expected: "some hash is 'h4sh'",
	}, {
		name:     "ci-operator duration minutes",
		message:  "...after 1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "ci-operator duration hours",
		message:  "...after 69h1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds duration",
		message:  "...PASS: TestRegistryProviderGet (2.83s)...",
		expected: "...PASS: TestRegistryProviderGet (<DURATION>)...",
	}, {
		name:     "ms duration",
		message:  "...PASS: TestRegistryProviderGet 510ms...",
		expected: "...PASS: TestRegistryProviderGet <DURATION>...",
	}, {
		name:     "spaced duration",
		message:  "...exited with code 1 after 00h 17m 40s...",
		expected: "...exited with code 1 after <DURATION>...",
	}, {
		name:     "ISO time",
		message:  "...time=\"2019-05-21T15:31:35Z\"...",
		expected: "...time=\"<ISO-DATETIME>\"...",
	}, {
		name:     "ISO DATE",
		message:  "...date=\"2019-05-21\"...",
		expected: "...date=\"<ISO-DATETIME>\"...",
	},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeMessage(tc.message); got != tc.expected {
				t.Errorf("sanitizeMessage('%s') = '%s', expected '%s'", tc.message, got, tc.expected)
			}
		})
	}
}
