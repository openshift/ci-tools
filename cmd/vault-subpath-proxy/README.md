# vault-subpath-proxy

## What
Reverse proxy for Vault that adds two capabilities the upstream Vault server lacks:
1. **Subpath discovery:** when a user gets a 403 on a KV metadata list request, the proxy inspects their effective ACL permissions and injects accessible subpaths into the response, making folders visible in the Vault UI even without list permission on the parent.
2. **KV write validation and secret sync:** intercepts KV write requests (PUT/POST/PATCH/DELETE), validates secret keys against Kubernetes naming rules, checks for conflicting keys across secrets targeting the same namespace/name, and asynchronously syncs the secret data to Kubernetes clusters.

## How it works -- full flow

### Subpath injection (ModifyResponse)
1. The proxy forwards all requests to the upstream Vault server.
2. On the response path, if the response is a **403** to a **GET** request on a KV **metadata** path with `?list=true`:
   - Reads and buffers the original response body.
   - Checks if Vault already returned data (non-empty `keys` array). If so, does nothing.
   - Extracts the user's Vault token from the `X-Vault-Token` header.
   - Calls Vault's `/v1/sys/internal/ui/resultant-acl` API with the user's token to get their effective permissions.
   - Scans the glob paths in the ACL result for paths that:
     - Start with the requested folder's metadata prefix
     - End with `/` (indicating a folder)
     - Have the `list` capability
   - If any matching folders are found, replaces the 403 response with a 200 containing the discovered subpaths as `keys`.

### KV write validation and sync (RoundTripper)
For PUT/POST/PATCH requests to KV paths:

1. **Key validation.** Reads and parses the request body as `{"data": {...}}`. For each key:
   - `secretsync/target-namespace`: validates each comma-separated namespace is a valid DNS-1123 label.
   - `secretsync/target-name`: validates as DNS-1123 label.
   - `secretsync/target-clusters`: skipped (no validation needed).
   - `secretsync/target-labels`: validates comma-separated `key:value` format; each key must be a valid qualified name and each value a valid Kubernetes label value.
   - All other keys: must match `^[a-zA-Z0-9\.\-_]+$`.
   - Validates the secret can be used in a CI step (`ci_validation.ValidateSecretInStep`).

2. **Conflict detection.** If a privileged Vault client is configured:
   - At startup (during `initialize()`), populates a key cache by listing all KV entries recursively and building a map of `{namespace, name} -> set of keys` and `vaultPath -> [{namespace, name, key}]`.
   - For each key in the request, checks if the same key is already claimed by a *different* Vault secret targeting the same Kubernetes namespace/name. This prevents two Vault entries from writing conflicting keys to the same Kubernetes Secret.
   - The cache is updated after successful writes and deletes.

3. **Upstream forwarding.** If validation passes, forwards the request to Vault.

4. **Secret sync.** On any write attempt (after forwarding to Vault, before the response status is checked), asynchronously syncs the secret data to all Kubernetes clusters:
   - Iterates all configured Kubernetes clients.
   - For each cluster that the secret targets (per `secretsync/target-clusters`), and for each target namespace (comma-separated):
     - Creates or updates a Kubernetes Secret with the data keys from the Vault entry.
     - Uses `controllerutil.CreateOrUpdate` with retry on conflict.
   - Sync has a 5-minute timeout per operation.

For DELETE requests:
- Forwards to Vault, then clears the key cache entry for the deleted path.

### TLS support
Supports serving over TLS with certificate hot-reloading: the cert/key pair is reloaded from disk every hour.

### Kubeconfig hot-reloading
Kubernetes clients are loaded at startup and reloaded via fsnotify when the kubeconfig file changes. Client access is protected by a read-write mutex.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--vault-addr` | `http://127.0.0.1:8300` | Upstream Vault address |
| `--kv-mount-path` | `secret` | KV secret engine mount path |
| `--listen-addr` | `127.0.0.1:8400` | Proxy listen address |
| `--tls-cert-file` | `""` | TLS certificate file path (requires `--tls-key-file`) |
| `--tls-key-file` | `""` | TLS key file path (requires `--tls-cert-file`) |
| `--vault-token` | `""` | Privileged Vault token for conflict detection (mutually exclusive with `--vault-role`) |
| `--vault-role` | `""` | Vault role for Kubernetes SA auth for conflict detection (mutually exclusive with `--vault-token`) |
| `--read-only` | `false` | Reject all write operations to the KV store (use during Vault-to-GSM migration to freeze secrets) |
| Prow kubernetes flags | -- | Multi-cluster kubeconfig for secret sync (`--kubeconfig`, etc.). Default: no in-cluster config. |

## Key files
- `cmd/vault-subpath-proxy/main.go` -- server setup, reverse proxy creation, subpath injection logic, TLS reloader, kubeconfig loading
- `cmd/vault-subpath-proxy/kv_update_transport.go` -- KV write interception: key validation, conflict detection, key cache management, Kubernetes secret sync

## Deployment
Runs as a sidecar container inside the `vault` StatefulSet on app.ci (namespace `vault`, 3 replicas), not a standalone Deployment. Listens on port 8300 for TLS termination, forwarding to Vault at `127.0.0.1:8200`. Requires:
- Network access to the upstream Vault server.
- A privileged Vault token or role with read access to the entire KV store (for conflict detection).
- Kubeconfig access to build clusters (for secret sync).
- TLS cert/key if serving HTTPS.

## Related
- `cmd/vault-secret-collection-manager` -- the UI that users interact with to manage secret collections; this proxy sits between the Vault UI/CLI and the actual Vault server.
- `pkg/api/vault/` -- constants for secret sync target keys (`secretsync/target-namespace`, etc.)
Careful: The `resultant-acl` api is internal, undocumented and no stability guarantee is provided. Ideally, this
functionality will get included into Vault itself one day.
