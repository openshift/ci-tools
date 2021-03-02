package testhelper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

const VaultTestingRootToken = "jpuxZFWWFW7vM882GGX2aWOE"

// Vault constructs a running Vault instance ready for testing. It returns its addess.
// VaultTestingRootToken is the initial root token.
func Vault(ctx context.Context, t *T) string {
	if _, err := exec.LookPath("vault"); err != nil {
		if _, runningInCi := os.LookupEnv("CI"); runningInCi {
			t.Fatalf("could not find vault in path: %v", err)
		}
		t.Skip("could not find vault in path")
		return "" // Unreachable code
	}

	// Vault writes the .vault-token file in there, do not mess with users home
	// and make sure that this is always writeable.
	os.Setenv("HOME", t.TempDir())

	var vaultListenPort string
	vault := NewAccessory("vault", []string{"server", "-dev", fmt.Sprintf("-dev-root-token-id=%s", VaultTestingRootToken)},
		func(port, _ string) []string {
			vaultListenPort = port
			return []string{fmt.Sprintf("--dev-listen-address=127.0.0.1:%s", vaultListenPort)}
		},
		nil,
	)
	vault.Run(t, ctx)
	vault.Ready(t, func(o *ReadyOptions) { o.ReadyURL = fmt.Sprintf("http://127.0.0.1:%s/v1/sys/health", vaultListenPort) })

	return "127.0.0.1:" + vaultListenPort
}
