package secrets

import (
	"errors"
	"flag"
	"fmt"

	"github.com/openshift/ci-tools/pkg/vaultclient"
)

type CLIOptions struct {
	VaultTokenFile string
	VaultAddr      string
	VaultPrefix    string
	VaultRole      string

	VaultToken string
}

func (o *CLIOptions) Bind(fs *flag.FlagSet, getenv func(string) string, censor *DynamicCensor) {
	fs.StringVar(&o.VaultAddr, "vault-addr", "", "Address of the vault endpoint. Defaults to the VAULT_ADDR env var if unset. Mutually exclusive with --bw-user and --bw-password-path.")
	fs.StringVar(&o.VaultTokenFile, "vault-token-file", "", "Token file to use when interacting with Vault, defaults to the VAULT_TOKEN env var if unset. Mutually exclusive with --bw-user and --bw-password-path.")
	fs.StringVar(&o.VaultPrefix, "vault-prefix", "", "Prefix under which to operate in Vault. Mandatory when using vault.")
	fs.StringVar(&o.VaultRole, "vault-role", "", "The vault role to use for Kubernetes auth. When passed and no token is passed, login via Kubernetes auth will be attempted.")
	o.VaultAddr = getenv("VAULT_ADDR")
	if v := getenv("VAULT_TOKEN"); v != "" {
		censor.AddSecrets(v)
		o.VaultToken = v
	}
}

func (o *CLIOptions) Validate() error {
	if o.VaultAddr == "" || (o.VaultToken == "" && o.VaultTokenFile == "" && o.VaultRole == "") || o.VaultPrefix == "" {
		return errors.New("--vault-addr, one of --vault-token, the VAULT_TOKEN env var or --vault-role and --vault-prefix must be specified together")
	}
	return nil
}

func (o *CLIOptions) Complete(censor *DynamicCensor) error {
	if o.VaultTokenFile != "" {
		var err error
		if o.VaultToken, err = ReadFromFile(o.VaultTokenFile, censor); err != nil {
			return err
		}
	}
	return nil
}

func (o *CLIOptions) NewReadOnlyClient(censor *DynamicCensor) (ReadOnlyClient, error) {
	return o.NewClient(censor)
}

func (o *CLIOptions) NewClient(censor *DynamicCensor) (Client, error) {
	var c *vaultclient.VaultClient
	var err error
	if o.VaultRole != "" {
		c, err = vaultclient.NewFromKubernetesAuth(o.VaultAddr, o.VaultRole)
	} else {
		c, err = vaultclient.New(o.VaultAddr, o.VaultToken)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to construct vault client: %w", err)
	}
	return NewVaultClient(c, o.VaultPrefix, censor), nil
}
