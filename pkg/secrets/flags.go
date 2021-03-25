package secrets

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/bitwarden"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

type CLIOptions struct {
	BwUser         string
	BwPasswordPath string
	VaultTokenFile string
	VaultAddr      string
	VaultPrefix    string

	BwPassword string
	VaultToken string
}

func (o *CLIOptions) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.BwUser, "bw-user", "", "Username to access BitWarden.")
	fs.StringVar(&o.BwPasswordPath, "bw-password-path", "", "Path to a password file to access BitWarden.")
	fs.StringVar(&o.VaultAddr, "vault-addr", "", "Address of the vault endpoint. Defaults to the VAULT_ADDR env var if unset. Mutually exclusive with --bw-user and --bw-password-path.")
	fs.StringVar(&o.VaultTokenFile, "vault-token-file", "", "Token file to use when interacting with Vault, defaults to the VAULT_TOKEN env var if unset. Mutually exclusive with --bw-user and --bw-password-path.")
	fs.StringVar(&o.VaultPrefix, "vault-prefix", "", "Prefix under which to operate in Vault. Mandatory when using vault.")
}

func (o *CLIOptions) Validate() []error {
	var errs []error
	var credentialsProviderConfigured []string
	if (o.BwUser == "") != (o.BwPasswordPath == "") {
		errs = append(errs, fmt.Errorf("--bw-user and --bw-password-path must be specified together"))
	} else if o.BwUser != "" {
		credentialsProviderConfigured = append(credentialsProviderConfigured, "bitwarden")
	}

	if vals := sets.NewString(o.VaultAddr, o.VaultTokenFile, o.VaultPrefix); len(vals) != 1 && ((len(vals) != 3) || vals.Has("")) {
		errs = append(errs, fmt.Errorf("--vault-addr, --vault-token and --vault-prefix must be specified together"))
	} else if len(vals) == 3 {
		credentialsProviderConfigured = append(credentialsProviderConfigured, "vault")
	}

	if len(credentialsProviderConfigured) != 1 {
		errs = append(errs, fmt.Errorf("must specify credentials for exactly one of vault or bitwarden, got credentials for: %v", credentialsProviderConfigured))
	}
	return errs
}

func (o *CLIOptions) Complete(censor *DynamicCensor) error {
	if o.BwPasswordPath != "" {
		bytes, err := ioutil.ReadFile(o.BwPasswordPath)
		if err != nil {
			return err
		}
		o.BwPassword = strings.TrimSpace(string(bytes))
		censor.AddSecrets(o.BwPassword)
	}
	if o.VaultAddr == "" {
		o.VaultAddr = os.Getenv("VAULT_ADDR")
	}
	o.VaultToken = os.Getenv("VAULT_TOKEN")
	if o.VaultTokenFile != "" {
		raw, err := ioutil.ReadFile(o.VaultTokenFile)
		if err != nil {
			return err
		}
		o.VaultToken = strings.TrimSpace(string(raw))
	}
	censor.AddSecrets(o.VaultToken)
	return nil
}

func (o *CLIOptions) NewClient(censor *DynamicCensor) (Client, error) {
	if o.BwUser != "" {
		c, err := bitwarden.NewClient(o.BwUser, o.BwPassword, censor.AddSecrets)
		if err != nil {
			return nil, fmt.Errorf("Failed to get Bitwarden client: %w", err)
		}
		return NewBitwardenClient(c), nil
	} else {
		c, err := vaultclient.New(o.VaultAddr, o.VaultToken)
		if err != nil {
			return nil, fmt.Errorf("Failed to construct vault client: %w", err)
		}
		return NewVaultClient(c, o.VaultPrefix, censor), nil
	}
}
