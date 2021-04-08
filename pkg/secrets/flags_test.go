package secrets

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestParseOptions(t *testing.T) {
	testCases := []struct {
		name     string
		given    []string
		env      map[string]string
		expected CLIOptions
	}{
		{
			name: "basic case",
			given: []string{
				"--bw-user=username",
				"--bw-password-path=/tmp/bw-password",
			},
			expected: CLIOptions{
				BwUser:         "username",
				BwPasswordPath: "/tmp/bw-password",
			},
		},
		{
			name:     "vault address from environment",
			env:      map[string]string{"VAULT_ADDR": "vault address"},
			expected: CLIOptions{VaultAddr: "vault address"},
		},
		{
			name:     "vault token from environment",
			env:      map[string]string{"VAULT_TOKEN": "vault token"},
			expected: CLIOptions{VaultToken: "vault token"},
		},
	}
	censor := NewDynamicCensor()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ExitOnError)
			actual := CLIOptions{}
			actual.Bind(fs, func(s string) string { return tc.env[s] }, &censor)
			if err := fs.Parse(tc.given); err != nil {
				t.Fatalf("invalid arguments: %v", err)
			}
			if diff := cmp.Diff(actual, tc.expected); diff != "" {
				t.Fatalf("unexpected result: %s", diff)
			}
		})
	}
}

func TestValidateOptions(t *testing.T) {
	testCases := []struct {
		name     string
		given    CLIOptions
		expected []error
	}{
		{
			name: "basic case",
			given: CLIOptions{
				BwUser:         "username",
				BwPasswordPath: "/tmp/bw-password",
			},
		},
		{
			name: "vault basic case",
			given: CLIOptions{
				VaultAddr:      "vault addr",
				VaultTokenFile: "vault token file",
				VaultPrefix:    "vault prefix",
			},
		},
		{
			name: "vault token prepopulated from environment",
			given: CLIOptions{
				VaultAddr:   "vault addr",
				VaultToken:  "vault token",
				VaultPrefix: "vault prefix",
			},
		},
		{
			name: "empty bw user",
			given: CLIOptions{
				BwPasswordPath: "/tmp/bw-password",
			},
			expected: []error{
				fmt.Errorf("--bw-user and --bw-password-path must be specified together"),
				fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: []"),
			},
		},
		{
			name: "empty bw user password path",
			given: CLIOptions{
				BwUser: "username",
			},
			expected: []error{
				fmt.Errorf("--bw-user and --bw-password-path must be specified together"),
				fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: []"),
			},
		},
		{
			name: "empty vault address",
			given: CLIOptions{
				VaultToken:  "vault token",
				VaultPrefix: "vault prefix",
			},
			expected: []error{
				fmt.Errorf("--vault-addr, --vault-token and --vault-prefix must be specified together"),
				fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: []"),
			},
		},
		{
			name: "empty vault token",
			given: CLIOptions{
				VaultAddr:   "vault adrr",
				VaultPrefix: "vault prefix",
			},
			expected: []error{
				fmt.Errorf("--vault-addr, --vault-token and --vault-prefix must be specified together"),
				fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: []"),
			},
		},
		{
			name: "empty vault prefix",
			given: CLIOptions{
				VaultAddr:  "vault adrr",
				VaultToken: "vault token",
			},
			expected: []error{
				fmt.Errorf("--vault-addr, --vault-token and --vault-prefix must be specified together"),
				fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: []"),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.given.Validate()
			if diff := cmp.Diff(actual, tc.expected, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("unexpected error: %s", diff)
			}
		})
	}
}

//
func TestCompleteOptions(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Errorf("Failed to create temp dir")
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("Failed to remove temp dir")
		}
	}()

	bwPasswordPath := filepath.Join(dir, "bwPasswordPath")
	if err := ioutil.WriteFile(bwPasswordPath, []byte("topSecret"), 0755); err != nil {
		t.Errorf("Failed to remove temp dir")
	}
	testCases := []struct {
		name               string
		given              CLIOptions
		expectedError      error
		expectedBWPassword string
	}{
		{
			name: "basic case",
			given: CLIOptions{
				BwUser:         "username",
				BwPasswordPath: bwPasswordPath,
			},
			expectedBWPassword: "topSecret",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			censor := NewDynamicCensor()
			actualError := tc.given.Complete(&censor)
			if diff := cmp.Diff(actualError, tc.expectedError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("unexpected error: %s", diff)
			}
			if diff := cmp.Diff(tc.given.BwPassword, tc.expectedBWPassword, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("unexpected Bitwarden password: %s", diff)
			}
		})
	}
}
