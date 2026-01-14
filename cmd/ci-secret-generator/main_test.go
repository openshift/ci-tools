package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

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
		name             string
		config           secretgenerator.Config
		disabledClusters sets.Set[string]
		expected         map[string]map[string]string
		unexpectedKeys   []string
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
	}, {
		name: "multiple items with the different names and disabled clusters",
		config: secretgenerator.Config{
			{
				ItemName: "attachment",
				Fields: []secretgenerator.FieldGenerator{
					{
						Name:    "name",
						Cmd:     "printf 'attachment content'",
						Cluster: "build01",
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
		disabledClusters: sets.New[string]("build01"),
		expected: map[string]map[string]string{
			"secret/prefix/field": {
				"name": "field content",
			},
			"secret/prefix/notes": {
				"notes": "notes content",
			},
		},
		unexpectedKeys: []string{"secret/prefix/attachment"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				for k := range tc.expected {
					if err := vault.DestroyKVIrreversibly(k); err != nil {
						t.Errorf("failed to delete key %q: %v", k, err)
					}
				}
			}()
			if err := updateSecrets(tc.config, client, tc.disabledClusters, false); err != nil {
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
			for _, k := range tc.unexpectedKeys {
				_, err := vault.GetKV(k)
				if err == nil {
					t.Fatalf("get an unexpected key: %q", k)
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

func TestFmtExecCmdErr(t *testing.T) {
	testCases := []struct {
		name           string
		action         string
		cmd            string
		wrapErr        error
		stdout         []byte
		stderr         []byte
		partialStreams bool
		expected       error
	}{
		{
			"no stdout and stderr",
			"run", "echo", errors.New("wrapped"), []byte{}, []byte{}, false,
			fmt.Errorf(execCmdErrFmt, "run", "echo", errors.New("wrapped"), "output", "",
				"error output", ""),
		},
		{
			"stdout and stderr exist",
			"run", "echo", errors.New("wrapped"), []byte("test out"), []byte("test err"), false,
			fmt.Errorf(execCmdErrFmt, "run", "echo", errors.New("wrapped"), "output", "test out",
				"error output", "test err"),
		},
		{
			"no error",
			"run", "echo", nil, []byte("test out"), []byte("test err"), false,
			fmt.Errorf(execCmdErrFmt, "run", "echo", nil, "output", "test out",
				"error output", "test err"),
		},
		{
			"partial streams",
			"run", "false", errors.New("wrapped"), []byte("stdou..."), []byte("stder..."), true,
			fmt.Errorf(execCmdErrFmt, "run", "false", errors.New("wrapped"),
				"output (may be incomplete)", "stdou...", "error output (may be incomplete)", "stder..."),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := fmtExecCmdErr(tc.action, tc.cmd, tc.wrapErr, tc.stdout, tc.stderr, tc.partialStreams)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", tc.name, diff)
			}
		})
	}
}

func TestExecuteCommand(t *testing.T) {
	testCases := []struct {
		name          string
		cmd           string
		expected      []byte
		expectedError error
	}{
		{
			name:     "basic case",
			cmd:      "echo basic case",
			expected: []byte("basic case\n"),
		},
		{
			name: "error on no output",
			cmd:  "true",
			expectedError: errors.New(
				`failed to validate stdout of command "true": no output returned
output:

error output:
`),
		},
		{
			name: "error on cmd failure",
			cmd:  "false",
			expectedError: errors.New(
				`failed to run command "false": exit status 1
output:

error output:
`),
		},
		{
			name: "error if stderr is not empty",
			cmd:  ">&2 echo some error",
			expectedError: errors.New(
				`failed to validate stderr of command ">&2 echo some error": stderr is not empty
output:

error output:
some error
`),
		},
		{
			name: "error if stdout is 'null'",
			cmd:  "echo null",
			expectedError: errors.New(
				`failed to validate stdout of command "echo null": 'null' output returned
output:
null

error output:
`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := executeCommand(tc.cmd)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: mismatch (-expected +actual), diff: %s", tc.name, diff)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	testcases := []struct {
		name           string
		expected       error
		expectedConfig secretgenerator.Config
	}{
		{
			name:     "no cluster param",
			expected: fmt.Errorf(`failed to find params['cluster'] in the 0 item with name "Item1"`),
		},
		{
			name: "valid",
			expectedConfig: secretgenerator.Config{
				{
					ItemName: "Item1",
					Fields:   []secretgenerator.FieldGenerator{{Name: "Attachment1", Cmd: "echo -n Attachment1", Cluster: "app.ci"}},
					Params:   map[string][]string{"cluster": {"app.ci"}},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := secretgenerator.LoadConfigFromPath(filepath.Join("testdata", fmt.Sprintf("%s.yaml", t.Name())))
			if err != nil {
				t.Errorf("unexpected error while loading confg from path: %v", err)
				t.FailNow()
			}
			o := options{config: config}
			if diff := cmp.Diff(tc.expected, o.validateConfig(), testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expected == nil {
				if diff := cmp.Diff(tc.expectedConfig, config); diff != "" {
					t.Errorf("Error differs from expected:\n%s", diff)
				}
			}
		})
	}
}

func TestBuildSecretsToUpdate(t *testing.T) {
	testCases := []struct {
		name                string
		config              secretgenerator.Config
		GSMsyncEnabled      bool
		disabledClusters    sets.Set[string]
		expectedItems       map[string]ItemUpdateInfo
		expectedIndexFields []string
		expectError         bool
	}{
		{
			name: "GSM sync enabled - single item, single field",
			config: secretgenerator.Config{{
				ItemName: "test-item",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "printf 'value1'"},
				},
			}},
			GSMsyncEnabled: true,
			expectedItems: map[string]ItemUpdateInfo{
				"test-item": {
					ItemName: "test-item",
					Fields: []FieldUpdateInfo{
						{FieldName: "field1", Payload: []byte("value1")},
					},
				},
			},
			expectedIndexFields: []string{"test-item__field1"},
		},
		{
			name: "GSM sync enabled - single item, multiple fields",
			config: secretgenerator.Config{{
				ItemName: "aws",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "printf 'value1'"},
					{Name: "field2", Cmd: "printf 'value2'"},
					{Name: "field3", Cmd: "printf 'value3'"},
				},
			}},
			GSMsyncEnabled: true,
			expectedItems: map[string]ItemUpdateInfo{
				"aws": {
					ItemName: "aws",
					Fields: []FieldUpdateInfo{
						{FieldName: "field1", Payload: []byte("value1")},
						{FieldName: "field2", Payload: []byte("value2")},
						{FieldName: "field3", Payload: []byte("value3")},
					},
				},
			},
			expectedIndexFields: []string{"aws__field1", "aws__field2", "aws__field3"},
		},
		{
			name: "GSM sync enabled - multiple items",
			config: secretgenerator.Config{
				{
					ItemName: "group1",
					Fields: []secretgenerator.FieldGenerator{
						{Name: "fieldA", Cmd: "printf 'valueA'"},
					},
				},
				{
					ItemName: "group2",
					Fields: []secretgenerator.FieldGenerator{
						{Name: "fieldA", Cmd: "printf 'valueA'"},
						{Name: "fieldB", Cmd: "printf 'valueB'"},
					},
				},
			},
			GSMsyncEnabled: true,
			expectedItems: map[string]ItemUpdateInfo{
				"group1": {
					ItemName: "group1",
					Fields: []FieldUpdateInfo{
						{FieldName: "fieldA", Payload: []byte("valueA")},
					},
				},
				"group2": {
					ItemName: "group2",
					Fields: []FieldUpdateInfo{
						{FieldName: "fieldA", Payload: []byte("valueA")},
						{FieldName: "fieldB", Payload: []byte("valueB")},
					},
				},
			},
			expectedIndexFields: []string{"group1__fieldA", "group2__fieldA", "group2__fieldB"},
		},
		{
			name: "GSM sync disabled - empty index",
			config: secretgenerator.Config{{
				ItemName: "test-item",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "printf 'value1'"},
					{Name: "field2", Cmd: "printf 'value2'"},
				},
			}},
			GSMsyncEnabled: false,
			expectedItems: map[string]ItemUpdateInfo{
				"test-item": {
					ItemName: "test-item",
					Fields: []FieldUpdateInfo{
						{FieldName: "field1", Payload: []byte("value1")},
						{FieldName: "field2", Payload: []byte("value2")},
					},
				},
			},
			expectedIndexFields: []string{}, // Empty when GSM sync disabled
		},
		{
			name: "GSM sync enabled - disabled cluster fields are excluded",
			config: secretgenerator.Config{{
				ItemName: "cluster-test",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "printf 'value1'", Cluster: "enabled-cluster"},
					{Name: "field2", Cmd: "printf 'value2'", Cluster: "disabled-cluster"},
					{Name: "field3", Cmd: "printf 'value3'", Cluster: "enabled-cluster"},
				},
			}},
			GSMsyncEnabled:   true,
			disabledClusters: sets.New[string]("disabled-cluster"),
			expectedItems: map[string]ItemUpdateInfo{
				"cluster-test": {
					ItemName: "cluster-test",
					Fields: []FieldUpdateInfo{
						{FieldName: "field1", Payload: []byte("value1")},
						{FieldName: "field3", Payload: []byte("value3")},
					},
				},
			},
			expectedIndexFields: []string{"cluster-test__field1", "cluster-test__field3"},
		},
		{
			name: "GSM sync enabled - names with forbidden characters are normalized in index",
			config: secretgenerator.Config{{
				ItemName: "aws/config",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "config.json", Cmd: "printf 'value1'"},
					{Name: "auth_token", Cmd: "printf 'value2'"},
				},
			}},
			GSMsyncEnabled: true,
			expectedItems: map[string]ItemUpdateInfo{
				"aws/config": {
					ItemName: "aws/config",
					Fields: []FieldUpdateInfo{
						{FieldName: "config.json", Payload: []byte("value1")},
						{FieldName: "auth_token", Payload: []byte("value2")},
					},
				},
			},
			expectedIndexFields: []string{"aws--slash--config__config--dot--json", "aws--slash--config__auth--u--token"},
		},
		{
			name: "GSM sync enabled - item with notes",
			config: secretgenerator.Config{{
				ItemName: "test-item",
				Notes:    "test notes content",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "printf 'value1'"},
				},
			}},
			GSMsyncEnabled: true,
			expectedItems: map[string]ItemUpdateInfo{
				"test-item": {
					ItemName: "test-item",
					Notes:    "test notes content",
					Fields: []FieldUpdateInfo{
						{FieldName: "field1", Payload: []byte("value1")},
					},
				},
			},
			expectedIndexFields: []string{"test-item__field1"},
		},
		{
			name: "GSM sync enabled - command execution failure",
			config: secretgenerator.Config{{
				ItemName: "test-item",
				Fields: []secretgenerator.FieldGenerator{
					{Name: "field1", Cmd: "false"},
				},
			}},
			GSMsyncEnabled:      true,
			expectedItems:       map[string]ItemUpdateInfo{},
			expectedIndexFields: []string{},
			expectError:         true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			itemsToUpdate, indexFields, err := buildSecretsToUpdate(tc.config, tc.disabledClusters, tc.GSMsyncEnabled)

			if tc.expectError && err == nil {
				t.Errorf("expected error but got nil")
				return
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if tc.expectError {
				return
			}

			if len(itemsToUpdate) != len(tc.expectedItems) {
				t.Errorf("expected %d items, got %d", len(tc.expectedItems), len(itemsToUpdate))
				return
			}

			actualItems := make(map[string]ItemUpdateInfo)
			for _, item := range itemsToUpdate {
				actualItems[item.ItemName] = item
			}

			if diff := cmp.Diff(tc.expectedItems, actualItems); diff != "" {
				t.Errorf("%s: mismatch (-expected, +actual):\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(tc.expectedIndexFields, indexFields); diff != "" {
				t.Errorf("%s: mismatch (-expected, +actual):\n%s", tc.name, diff)
			}
		})
	}
}
