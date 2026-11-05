package csi_secrets

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/secrets"
)

func TestAddYAMLValuesToCensor(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		input    string
		expected string
	}{
		{
			name:     "single key YAML value is censored",
			payload:  []byte("key1: supersecretvalue\n"),
			input:    "key1 via sed: supersecretvalue",
			expected: "key1 via sed: XXXXXXXXXXXXXXXX",
		},
		{
			name:     "multiple YAML values are all censored",
			payload:  []byte("key1: value1\nkey2: value2\nkey3: value3\n"),
			input:    "key1=value1 key2=value2 key3=value3",
			expected: "key1=XXXXXX key2=XXXXXX key3=XXXXXX",
		},
		{
			name:     "non-YAML payload is silently ignored",
			payload:  []byte("not yaml: [invalid"),
			input:    "no change expected",
			expected: "no change expected",
		},
		{
			name:     "non-string YAML values are ignored",
			payload:  []byte("count: 42\nname: secretname\n"),
			input:    "name=secretname count=42",
			expected: "name=XXXXXXXXXX count=42",
		},
		{
			name:     "empty string values are not added",
			payload:  []byte("key1: \nkey2: realvalue\n"),
			input:    "key2=realvalue",
			expected: "key2=XXXXXXXXX",
		},
		{
			name: "nested mapping values are censored",
			payload: []byte(`
kubeconfig:
  token: nestedtoken
  cluster:
    server: https://api.example.com
`),
			input:    "token=nestedtoken server=https://api.example.com",
			expected: "token=XXXXXXXXXXX server=XXXXXXXXXXXXXXXXXXXXXXX",
		},
		{
			name: "sequence values are censored",
			payload: []byte(`
passwords:
  - firstpassword
  - secondpassword
`),
			input:    "firstpassword secondpassword",
			expected: "XXXXXXXXXXXXX XXXXXXXXXXXXXX",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			censor := secrets.NewDynamicCensor()
			addYAMLValuesToCensor(tc.payload, &censor)
			result := []byte(tc.input)
			censor.Censor(&result)
			if string(result) != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, string(result))
			}
		})
	}
}
