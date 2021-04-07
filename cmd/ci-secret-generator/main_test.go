package main

import (
	"context"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
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

func TestBitwardenContextsFor(t *testing.T) {
	var testCases = []struct {
		in  []bitWardenItem
		out []secretbootstrap.BitWardenContext
	}{
		{
			in:  []bitWardenItem{},
			out: nil,
		},
		{
			in: []bitWardenItem{{
				ItemName: "item1",
				Fields: []fieldGenerator{{
					Name: "field1",
				}, {
					Name: "field2",
				}},
			}, {
				ItemName: "item2",
				Attachments: []fieldGenerator{{
					Name: "attachment1",
				}},
				Password: "whatever",
			}},
			out: []secretbootstrap.BitWardenContext{{
				BWItem: "item1",
				Field:  "field1",
			}, {
				BWItem: "item1",
				Field:  "field2",
			}, {
				BWItem:     "item2",
				Attachment: "attachment1",
			}, {
				BWItem:    "item2",
				Attribute: "password",
			}},
		},
	}

	for _, testCase := range testCases {
		if diff := cmp.Diff(testCase.out, bitwardenContextsFor(testCase.in)); diff != "" {
			t.Errorf("got incorrect output: %v", diff)
		}
	}
}

func TestVault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	vault, err := vaultclient.New("http://"+testhelper.Vault(ctx, testhelper.NewT(ctx, t)), testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to create Vault client: %v", err)
	}
	censor := secrets.NewDynamicCensor()
	prefix := "prefix/"
	client := secrets.NewVaultClient(vault, "secret/"+prefix, &censor)
	for _, tc := range []struct {
		name     string
		config   []bitWardenItem
		expected map[string]map[string]string
	}{{
		name: "single item",
		config: []bitWardenItem{{
			ItemName: "single_item",
			Attachments: []fieldGenerator{{
				Name: "name",
				Cmd:  "printf 'name content'",
			}},
		}},
		expected: map[string]map[string]string{
			"secret/prefix/single_item": {
				"name": "name content",
			},
		},
	}, {
		name: "multiple items with the same name",
		config: []bitWardenItem{{
			ItemName: "multiple_items",
			Attachments: []fieldGenerator{{
				Name: "attachment0",
				Cmd:  "printf 'attachment0 content'",
			}, {
				Name: "attachment1",
				Cmd:  "printf 'attachment1 content'",
			}},
			Fields: []fieldGenerator{{
				Name: "field",
				Cmd:  "printf 'field content'",
			}},
			Password: "printf 'password content'",
			Notes:    "notes content",
		}},
		expected: map[string]map[string]string{
			"secret/prefix/multiple_items": {
				"attachment0": "attachment0 content",
				"attachment1": "attachment1 content",
				"field":       "field content",
				"password":    "password content",
				"notes":       "notes content",
			},
		},
	}, {
		name: "multiple items with the different names",
		config: []bitWardenItem{{
			ItemName: "attachment",
			Attachments: []fieldGenerator{{
				Name: "name",
				Cmd:  "printf 'attachment content'",
			}},
		}, {
			ItemName: "field",
			Fields: []fieldGenerator{{
				Name: "name",
				Cmd:  "printf 'field content'",
			}},
		}, {
			ItemName: "password",
			Password: "printf 'password content'",
		}, {
			ItemName: "notes",
			Notes:    "notes content",
		}},
		expected: map[string]map[string]string{
			"secret/prefix/attachment": {
				"name": "attachment content",
			},
			"secret/prefix/field": {
				"name": "field content",
			},
			"secret/prefix/password": {
				"password": "password content",
			},
			"secret/prefix/notes": {
				"notes": "notes content",
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				for k := range tc.expected {
					if err := vault.DestroyKVIrreversibly(k); err != nil {
						t.Errorf("failed to delete key %q: %v", k, err)
					}
				}
			}()
			if err := updateSecrets(tc.config, client); err != nil {
				t.Errorf("failed to update secrets: %v", err)
			}
			list, err := vault.ListKV("secret")
			if err != nil {
				t.Errorf("failed to list Vault contents: %v", err)
			}
			if diff := cmp.Diff(list, []string{prefix}); diff != "" {
				t.Errorf("unexpected secret list: %s", diff)
			}
			for k, v := range tc.expected {
				secret, err := vault.GetKV(k)
				if err != nil {
					t.Fatalf("failed to get key %q: %v", k, err)
				}
				if diff := cmp.Diff(secret.Data, v); diff != "" {
					t.Errorf("unexpected secret content: %s", diff)
				}
			}
		})
	}
}
