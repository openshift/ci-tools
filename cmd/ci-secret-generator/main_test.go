package main

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestProcessBwParameters(t *testing.T) {
	testcases := []struct {
		name          string
		inputBwItems  []bitWardenItem
		expected      []bitWardenItem
		expectedError error
	}{
		{
			name: "No parameters",
			inputBwItems: []bitWardenItem{
				{
					ItemName: "Item1",
					Fields: []fieldGenerator{
						{
							Name: "Field1",
							Cmd:  "echo -n Field1",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1",
							Cmd:  "echo -n Attachment1",
						},
					},
					Password: "echo -n password1",
					Notes:    "Note1",
				},
			},
			expected: []bitWardenItem{
				{
					ItemName: "Item1",
					Fields: []fieldGenerator{
						{
							Name: "Field1",
							Cmd:  "echo -n Field1",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1",
							Cmd:  "echo -n Attachment1",
						},
					},
					Password: "echo -n password1",
					Notes:    "Note1",
				},
			},
		},
		{
			name: "Single parameter with single value",
			inputBwItems: []bitWardenItem{
				{
					ItemName: "Item$(FieldNum)",
					Fields: []fieldGenerator{
						{
							Name: "Field$(FieldNum)",
							Cmd:  "echo -n Field$(FieldNum)",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment$(FieldNum)",
							Cmd:  "echo -n Attachment$(FieldNum)",
						},
					},
					Password: "echo -n password$(FieldNum)",
					Notes:    "Note$(FieldNum)",
					Params: map[string][]string{
						"FieldNum": {
							"1",
						},
					},
				},
			},
			expected: []bitWardenItem{
				{
					ItemName: "Item1",
					Fields: []fieldGenerator{
						{
							Name: "Field1",
							Cmd:  "echo -n Field1",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1",
							Cmd:  "echo -n Attachment1",
						},
					},
					Password: "echo -n password1",
					Notes:    "Note1",
					Params: map[string][]string{
						"FieldNum": {
							"1",
						},
					},
				},
			},
		},
		{
			name: "Single parameter with multiple values",
			inputBwItems: []bitWardenItem{
				{
					ItemName: "Item$(FieldNum)",
					Fields: []fieldGenerator{
						{
							Name: "Field$(FieldNum)",
							Cmd:  "echo -n Field$(FieldNum)",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment$(FieldNum)",
							Cmd:  "echo -n Attachment$(FieldNum)",
						},
					},
					Password: "echo -n password$(FieldNum)",
					Notes:    "Note$(FieldNum)",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
					},
				},
			},
			expected: []bitWardenItem{
				{
					ItemName: "Item1",
					Fields: []fieldGenerator{
						{
							Name: "Field1",
							Cmd:  "echo -n Field1",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1",
							Cmd:  "echo -n Attachment1",
						},
					},
					Password: "echo -n password1",
					Notes:    "Note1",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
					},
				},
				{
					ItemName: "Item2",
					Fields: []fieldGenerator{
						{
							Name: "Field2",
							Cmd:  "echo -n Field2",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment2",
							Cmd:  "echo -n Attachment2",
						},
					},
					Password: "echo -n password2",
					Notes:    "Note2",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
					},
				},
			},
		},
		{
			name: "Two parameters with multiple values",
			inputBwItems: []bitWardenItem{
				{
					ItemName: "Item$(FieldNum)$(Env)",
					Fields: []fieldGenerator{
						{
							Name: "Field$(FieldNum)$(Env)",
							Cmd:  "echo -n Field$(FieldNum)$(Env)",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment$(FieldNum)$(Env)",
							Cmd:  "echo -n Attachment$(FieldNum)$(Env)",
						},
					},
					Password: "echo -n password$(FieldNum)$(Env)",
					Notes:    "Note$(FieldNum)$(Env)",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
						"Env": {
							"dev",
							"prod",
						},
					},
				},
			},
			expected: []bitWardenItem{
				{
					ItemName: "Item1dev",
					Fields: []fieldGenerator{
						{
							Name: "Field1dev",
							Cmd:  "echo -n Field1dev",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1dev",
							Cmd:  "echo -n Attachment1dev",
						},
					},
					Password: "echo -n password1dev",
					Notes:    "Note1dev",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
						"Env": {
							"dev",
							"prod",
						},
					},
				},
				{
					ItemName: "Item1prod",
					Fields: []fieldGenerator{
						{
							Name: "Field1prod",
							Cmd:  "echo -n Field1prod",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment1prod",
							Cmd:  "echo -n Attachment1prod",
						},
					},
					Password: "echo -n password1prod",
					Notes:    "Note1prod",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
						"Env": {
							"dev",
							"prod",
						},
					},
				},
				{
					ItemName: "Item2dev",
					Fields: []fieldGenerator{
						{
							Name: "Field2dev",
							Cmd:  "echo -n Field2dev",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment2dev",
							Cmd:  "echo -n Attachment2dev",
						},
					},
					Password: "echo -n password2dev",
					Notes:    "Note2dev",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
						"Env": {
							"dev",
							"prod",
						},
					},
				},

				{
					ItemName: "Item2prod",
					Fields: []fieldGenerator{
						{
							Name: "Field2prod",
							Cmd:  "echo -n Field2prod",
						},
					},
					Attachments: []fieldGenerator{
						{
							Name: "Attachment2prod",
							Cmd:  "echo -n Attachment2prod",
						},
					},
					Password: "echo -n password2prod",
					Notes:    "Note2prod",
					Params: map[string][]string{
						"FieldNum": {
							"1",
							"2",
						},
						"Env": {
							"dev",
							"prod",
						},
					},
				},
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			outputBwItems, err := processBwParameters(tc.inputBwItems)
			sort.Slice(outputBwItems, func(p, q int) bool {
				return outputBwItems[p].ItemName < outputBwItems[q].ItemName
			})
			equal(t, tc.expectedError, err)
			equal(t, tc.expected, outputBwItems)
		})
	}
}

func equal(t *testing.T, expected, actual interface{}) {
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("actual differs from expected:\n%s", cmp.Diff(expected, actual))
	}
}
