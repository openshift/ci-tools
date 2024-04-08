package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGetAllowList(t *testing.T) {
	testcases := []struct {
		name          string
		input         string
		expectedOut   map[string]string
		expectedError error
	}{
		{
			name: "Release type blocking",
			input: `
key1: informing
key2: blocking
`,
			expectedError: fmt.Errorf("release_type 'blocking' not permitted in the allow-list for key2, blocking jobs must be in the release controller configuration"),
			expectedOut: map[string]string{
				"key1": "informing",
				"key2": "blocking",
			},
		},
		{
			name: "Release type blocking",
			input: `
key1: informing
key2: informing
`,
			expectedOut: map[string]string{
				"key1": "informing",
				"key2": "informing",
			},
		},
		{
			name: "Release type empty",
			input: `
key1: 
key2: informing
`,
			expectedError: fmt.Errorf("key1: release_type must be non-empty"),
			expectedOut: map[string]string{
				"key1": "",
				"key2": "informing",
			},
		},
	}
	for _, tc := range testcases {
		data := tc.input
		allowList, err := getAllowList([]byte(data))
		equal(t, tc.expectedOut, allowList)
		equalError(t, tc.expectedError, err)
	}

}

func equalError(t *testing.T, expected, actual error) {
	if expected != nil && actual == nil || expected == nil && actual != nil {
		t.Errorf("expecting error \"%v\", got \"%v\"", expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("expecting error msg %q, got %q", expected.Error(), actual.Error())
	}
}

func equal(t *testing.T, expected, actual interface{}) {
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("actual differs from expected:\n%s", cmp.Diff(expected, actual))
	}
}
