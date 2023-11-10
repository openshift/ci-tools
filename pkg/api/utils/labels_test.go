package utils

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMungeLabels(t *testing.T) {
	var testCases = []struct {
		input, output map[string]string
	}{
		{
			input:  map[string]string{},
			output: map[string]string{},
		},
		{
			input: map[string]string{
				"a": "b",
				"b": "[b",
				"c": "b!",
				"d": "|b!",
				"e": "|b:b-b!",
				"f": "",
			},
			output: map[string]string{
				"a": "b",
				"b": "b",
				"c": "b",
				"d": "b",
				"e": "b_b-b",
				"f": "",
			},
		},
	}
	for i, testCase := range testCases {
		if diff := cmp.Diff(testCase.output, mungeLabels(testCase.input)); diff != "" {
			t.Errorf("case %d: got incorrect output: %v", i, diff)
		}
	}
}
