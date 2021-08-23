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

func TestItemContextsFromConfig(t *testing.T) {
	var testCases = []struct {
		in  secretgenerator.Config
		out []secretbootstrap.ItemContext
	}{
		{
			in:  secretgenerator.Config{},
			out: nil,
		},
		{
			in: secretgenerator.Config{
				{
					ItemName: "item1",
					Fields:   []secretgenerator.FieldGenerator{{Name: "field1"}, {Name: "field2"}},
				},
				{
					ItemName: "item2",
					Fields:   []secretgenerator.FieldGenerator{{Name: "field1"}},
				},
			},
			out: []secretbootstrap.ItemContext{
				{
					Item:  "item1",
					Field: "field1",
				},
				{
					Item:  "item1",
					Field: "field2",
				},
				{
					Item:  "item2",
					Field: "field1",
				},
			},
		},
	}

	for _, testCase := range testCases {
		if diff := cmp.Diff(testCase.out, itemContextsFromConfig(testCase.in)); diff != "" {
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
			Fields: []secretgenerator.FieldGenerator{{
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
			Fields: []secretgenerator.FieldGenerator{{
				Name: "attachment0",
				Cmd:  "printf 'attachment0 content'",
			}, {
				Name: "attachment1",
				Cmd:  "printf 'attachment1 content'",
			}},
			Notes: "notes content",
		}},
		expected: map[string]map[string]string{
			"secret/prefix/multiple_items": {
				"attachment0": "attachment0 content",
				"attachment1": "attachment1 content",
				"notes":       "notes content",
			},
		},
	}, {
		name: "multiple items with the different names",
		config: secretgenerator.Config{
			{
				ItemName: "attachment",
				Fields: []secretgenerator.FieldGenerator{
					{
						Name: "name",
						Cmd:  "printf 'attachment content'",
					},
				},
			},
			{
				ItemName: "field",
				Fields: []secretgenerator.FieldGenerator{
					{
						Name: "name",
						Cmd:  "printf 'field content'",
					},
				},
			},
			{
				ItemName: "notes",
				Notes:    "notes content",
			},
		},
		expected: map[string]map[string]string{
			"secret/prefix/attachment": {
				"name": "attachment content",
			},
			"secret/prefix/field": {
				"name": "field content",
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

func TestValidateContexts(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		cfg          secretgenerator.Config
		bootstrapCfg secretbootstrap.Config
	}{
		{
			name: "Directly found",
			cfg:  secretgenerator.Config{{ItemName: "some-item", Fields: []secretgenerator.FieldGenerator{{Name: "field"}}}},
			bootstrapCfg: secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{
				From: map[string]secretbootstrap.ItemContext{"": {Item: "some-item", Field: "field"}},
			}}},
		},
		{
			name: "Directly found dockerconfigjson",
			cfg:  secretgenerator.Config{{ItemName: "some-item", Fields: []secretgenerator.FieldGenerator{{Name: "field"}}}},
			bootstrapCfg: secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{
				From: map[string]secretbootstrap.ItemContext{"": {DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{{
					Item: "some-item", AuthField: "field",
				}}}},
			}}},
		},
		{
			name: "Strip prefix",
			cfg:  secretgenerator.Config{{ItemName: "some-item", Fields: []secretgenerator.FieldGenerator{{Name: "field"}}}},
			bootstrapCfg: secretbootstrap.Config{
				VaultDPTPPrefix: "dptp",
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"": {Item: "some-item", Field: "field"}},
				}},
			},
		},
		{
			name: "Strip prefix dockerconfigjson",
			cfg:  secretgenerator.Config{{ItemName: "some-item", Fields: []secretgenerator.FieldGenerator{{Name: "field"}}}},
			bootstrapCfg: secretbootstrap.Config{
				VaultDPTPPrefix: "dptp",
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"": {DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{{
						Item: "dptp/some-item", AuthField: "field",
					}}}},
				}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateContexts(itemContextsFromConfig(tc.cfg), tc.bootstrapCfg); err != nil {
				t.Errorf("validation failed unexpectedly: %v", err)
			}
		})
	}
}
