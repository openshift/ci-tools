package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

func TestBitwardenContextsFor(t *testing.T) {
	var testCases = []struct {
		in  secretgenerator.Config
		out []secretbootstrap.BitWardenContext
	}{
		{
			in:  secretgenerator.Config{},
			out: nil,
		},
		{
			in: secretgenerator.Config{{
				ItemName: "item1",
				Fields: []secretgenerator.FieldGenerator{{
					Name: "field1",
				}, {
					Name: "field2",
				}},
			}, {
				ItemName: "item2",
				Attachments: []secretgenerator.FieldGenerator{{
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
	vault, err := vaultclient.New("http://"+testhelper.Vault(t), testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to create Vault client: %v", err)
	}
	censor := secrets.NewDynamicCensor()
	prefix := "prefix/"
	client := secrets.NewVaultClient(vault, "secret/"+prefix, &censor)
	for _, tc := range []struct {
		name     string
		config   secretgenerator.Config
		expected map[string]map[string]string
	}{{
		name: "single item",
		config: secretgenerator.Config{{
			ItemName: "single_item",
			Attachments: []secretgenerator.FieldGenerator{{
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
		config: secretgenerator.Config{{
			ItemName: "multiple_items",
			Attachments: []secretgenerator.FieldGenerator{{
				Name: "attachment0",
				Cmd:  "printf 'attachment0 content'",
			}, {
				Name: "attachment1",
				Cmd:  "printf 'attachment1 content'",
			}},
			Fields: []secretgenerator.FieldGenerator{{
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
		config: secretgenerator.Config{{
			ItemName: "attachment",
			Attachments: []secretgenerator.FieldGenerator{{
				Name: "name",
				Cmd:  "printf 'attachment content'",
			}},
		}, {
			ItemName: "field",
			Fields: []secretgenerator.FieldGenerator{{
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
