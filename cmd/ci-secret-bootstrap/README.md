# ci-secret-bootstrap

## What
Provisions secrets from Vault and Google Secret Manager (GSM) to Kubernetes clusters across the CI infrastructure. Reads a mapping config that defines which Vault items/fields map to which Kubernetes Secrets on which clusters, then creates or updates them.

## How it works — full flow

### 1. Configuration
The config file (`--config`) defines:
- **cluster_groups**: named groups of clusters (e.g., `build_farm: [app.ci, build01, build02]`)
- **secrets**: array of source-to-target mappings:
  - `from`: map of field names to Vault item+field references
  - `to`: array of target cluster/namespace/name/type specifications
  - Target can specify `cluster` (direct) or `cluster_groups` (expanded)
- **user_secrets_target_clusters**: clusters receiving self-service user secrets

### 2. Secret construction from Vault (`constructSecretsFromVault()`)
- Fetches all fields in parallel via goroutines
- Supports `.dockerconfigjson` construction from multiple registry auth fields
- Supports `base64_decode` for pre-encoded values
- User secrets fetched separately via `client.GetUserSecrets()` — self-service secrets with `secretsync/target-clusters` targeting

### 3. Secret construction from GSM (if `--enable-gsm`)
- Bundles define grouped secrets with components, docker configs, and targets
- Field auto-discovery: lists all fields in a collection/group if not explicitly specified
- `${CLUSTER}` variable substitution for cluster-specific secrets
- Component inheritance for reusability

### 4. Conflict detection
Prevents same secret (cluster/namespace/name) from being managed by both Vault and GSM. Vault takes precedence.

### 5. Writing to clusters (`updateSecrets()`)
- Creates namespace if missing
- For existing secrets: checks if update needed
- Handles immutable field changes (requires `--force`)
- Special case: OSD global pull secret (`openshift-config/pull-secret`) — mutates in-place, only updating specific registry entries
- All secrets get `ci-secret-bootstrap` label for tracking

### 6. Validation modes
- `--validate-only`: load and validate config, check Vault items exist, exit
- `--validate-bitwarden-items-usage`: report unused Vault items (older than 7 days)

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--config` | — | Vault bootstrap config file |
| `--vault-addr` | `VAULT_ADDR` env | Vault server address |
| `--vault-token-file` | `VAULT_TOKEN` env | Token file |
| `--vault-prefix` | — | Vault key path prefix |
| `--vault-role` | — | Kubernetes auth role (alternative to token) |
| `--dry-run` | true | Preview mode |
| `--confirm` | true | Actually mutate secrets (requires dry-run=false) |
| `--force` | false | Force update even if different |
| `--validate-only` | false | Exit after validation |
| `--cluster` | — | Only provision to this cluster |
| `--secret-names` | — | Only provision these secrets |
| `--enable-gsm` | false | Enable GSM bundle mechanism |
| `--gsm-config` | — | GSM config file |
| `--gsm-credentials-file` | — | GSM service account credentials |

## Key files
- `cmd/ci-secret-bootstrap/main.go` — orchestration, Vault/GSM construction, cluster writing
- `pkg/api/secretbootstrap/secretboostrap.go` — Config, SecretConfig, ItemContext types
- `pkg/secrets/flags.go` — Vault client options

## Deployment
Periodic Prow job ([recent runs](https://prow.ci.openshift.org/?job=periodic-ci-secret-bootstrap)). This tool extends the original [populate-secrets-from-bitwarden.sh](https://github.com/openshift/release/blob/c8c89d08c56c653b91eb8c7580657f7ce522253f/ci-operator/populate-secrets-from-bitwarden.sh) to support mirroring secrets across Kubernetes/OpenShift clusters.

---

## Additional details

### Args and config.yaml

We use `--kubeconfig` to specify the path to a [kube config](https://kubernetes.io/docs/concepts/configuration/organize-cluster-access-kubeconfig/)
that the tool will load and use to access clusters for writing secrets.

It expects a configuration like the one below which specifies the mapping from items
in the secrets backend (Vault) to the targeting Kubernetes secret.

```yaml
- from:
    key-name-1:
      item: item-name-1
      field: field-name-1
    key-name-2:
      item: item-name-1
      field: field-name-2
    key-name-3:
      item: item-name-2
      field: field-name-1
    key-name-4:
      item: item-name-3
      field: field-name-2
  to:
    - cluster: default
      namespace: namespace-1
      name: prod-secret-1
    - cluster: build01
      namespace: namespace-2
      name: prod-secret-2
```

where `cluster` is the `context` name in the `kubeconfig` (`oc config rename-context` to rename a context in `kubeconfig`):

* `default`: `https://api.ci.openshift.org:443`, and
* `build01`: `https://api.build01.ci.devcluster.openshift.com:6443`.

So the above configuration tells the tool to use the following data to
create a secret with its `key` as `secret.data.key` and the following as `secret.data.value`:

* `field`s of `field-name-1` and `field-name-2` from Vault item `item-name-1`,

* `field` of `field-name-1` from Vault item `item-name-2`, and

* `field` of `field-name-2` from Vault item `item-name-3`.

And then the secret will be populated to

* the `secret` `prod-secret-1` in `namespace-1` on the `default` cluster, and
* the `secret` `prod-secret-2` in `namespace-2` on the `build01` cluster.

Additionally, `.to.type` can be used to specify the [type of the secret](https://github.com/kubernetes/kubernetes/blob/07b358b1904c3c16a40a93a18f95e9411d9a2789/pkg/apis/core/types.go#L4753), such as `kubernetes.io/dockerconfigjson`.

> **Note:** This tool originally used Bitwarden as its secrets backend. It has since been migrated to use Vault and Google Secret Manager (GSM).

### Run

```bash
$ ci-secret-bootstrap --vault-addr=<vault_address> --vault-token-file=<path_to_token> --kubeconfig <path_to_kubeconfig_file> --config <path_to_config.yaml>
```

where `kubeconfig` contains the `contexts` for the `default` cluster and the `build01` cluster.
