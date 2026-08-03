# vault-secret-collection-manager

## What
Self-service web application for creating and managing isolated Vault secret collections with policy-based access control. Teams can create named collections, add/remove members, and store secrets -- all without DPTP intervention. Each collection is backed by a Vault KV path, a Vault identity group, and a Vault policy that grants the group CRUD access to that path.

## How it works -- full flow

### Authentication
The service expects an OAuth2 proxy (e.g., oauth2-proxy) in front. User identity is extracted from the `X-Forwarded-Email` header (the part before `@`). Requests without this header are rejected with HTTP 400.

### Secret collection lifecycle

#### Creating a collection (`PUT /secretcollection/:name`)
1. Validates the name matches `^[a-z0-9-]+$`.
2. Checks for an existing Vault group with the prefixed name (`secret-collection-manager-managed-<name>`). If it exists, returns 409 Conflict (idempotency is on group creation, not policy).
3. Looks up the requesting user by alias in Vault. If the user does not exist, creates a new Vault identity entity and alias for them.
4. Creates a Vault policy granting:
   - `list`, `delete` on `secret/metadata/self-managed/<name>/*`
   - `create`, `update`, `read` on `secret/data/self-managed/<name>/*`
5. Creates a Vault identity group named `secret-collection-manager-managed-<name>` with the requesting user as the sole member and the policy attached.
6. Creates a placeholder KV entry at `secret/self-managed/<name>/placeholder` so the collection is visible in the Vault web console (bypasses the censoring plugin's minimum-length rules).

#### Listing collections (`GET /secretcollection`)
1. Looks up the user by alias. Creates the user if they do not exist yet.
2. Iterates the user's group memberships, filtering for groups prefixed with `secret-collection-manager-managed`.
3. For each matching group, reads the group's policy to extract the collection path and resolves member names from entity IDs.
4. Returns a sorted JSON array of `{name, path, members}` objects.
5. If `?ui=true` is set, renders the embedded HTML template instead of raw JSON.

#### Updating members (`PUT /secretcollection/:name/members`)
1. Verifies the requesting user is a member of the collection (otherwise 404).
2. Accepts a JSON body: `{"members": ["user1", "user2"]}`. At least one member required.
3. Resolves each member name to a Vault entity ID (via alias lookup).
4. Calls `UpdateGroupMembers` to replace the group's member list.

#### Deleting a collection (`DELETE /secretcollection/:name`)
1. Verifies the requesting user is a member.
2. Lists all KV entries under the collection path recursively.
3. Irreversibly destroys each KV entry (not just soft-delete).
4. Deletes the Vault identity group.

### Policy reconciliation
Every hour, the manager reconciles all policies prefixed with `secret-collection-manager-managed`:
1. Lists all Vault policies.
2. For each managed policy, compares the current policy document against the expected one (re-derived from the collection name).
3. Updates any policies that have drifted.
4. Logs which policies were reconciled.

This runs at startup and then hourly via `interrupts.TickLiteral`.

### User management
- Users are auto-created on first access: a Vault identity entity is created with a `default` policy, and an alias is created linking the username to the configured auth backend (default: `oidc`).
- User and group lookups are cached in memory (`idNameCache`) to avoid redundant Vault API calls. The cache maps both name-to-ID and ID-to-name.

### Embedded frontend
The service embeds static assets (`style.css`, `index.js`, `index.template.html`) via Go's `//go:embed` directive. The root path `/` redirects to `/secretcollection?ui=true`.

### Endpoints

| Method | Path | What it does |
|---|---|---|
| `GET` | `/` | Redirects to `/secretcollection?ui=true` |
| `GET` | `/style.css` | Serves embedded CSS |
| `GET` | `/index.js` | Serves embedded JavaScript |
| `GET` | `/healthz` | Health check (200 OK) |
| `GET` | `/secretcollection` | List collections for the authenticated user. `?ui=true` returns HTML. |
| `PUT` | `/secretcollection/:name` | Create a new collection |
| `PUT` | `/secretcollection/:name/members` | Update collection members |
| `DELETE` | `/secretcollection/:name` | Delete a collection and all its secrets |
| `GET` | `/users` | List all Vault user aliases |

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--kv-store-prefix` | `secret/self-managed` | Vault KV folder under which all collections are created |
| `--listen-addr` | `127.0.0.1:8080` | Address to listen on |
| `--vault-addr` | `http://127.0.0.1:8300` | Upstream Vault address |
| `--vault-token` | `""` | Privileged Vault token (mutually exclusive with `--vault-role`) |
| `--vault-role` | `""` | Vault role for Kubernetes SA auth (mutually exclusive with `--vault-token`) |
| `--auth-backend-type` | `oidc` | Vault auth backend type used for user authentication |
| Instrumentation flags | -- | `--health-port`, `--metrics-port` (standard Prow instrumentation) |

## Key files
- `cmd/vault-secret-collection-manager/main.go` -- server setup, all HTTP handlers, collection CRUD, policy reconciliation, user/group management
- `cmd/vault-secret-collection-manager/types.go` -- data types: `secretCollection`, `managedVaultPolicy`, request/response bodies
- `cmd/vault-secret-collection-manager/middleware.go` -- logging, instrumentation (Prometheus histograms), UUID-based request tracking
- `cmd/vault-secret-collection-manager/frontend.go` -- embedded static assets via `//go:embed`
- `pkg/vaultclient/` -- Vault client library (identity, group, KV, policy operations)

## Deployment
Long-lived Deployment on app.ci, namespace `ci`. Fronted by an OAuth2 proxy that handles authentication and sets the `X-Forwarded-Email` header. Exposes a health endpoint on the instrumentation health port and Prometheus metrics on the metrics port.

If using `--vault-role`, authenticates to Vault via Kubernetes service account auth and monitors credential expiry (health check returns 500 when expired).

## Related
- `cmd/vault-subpath-proxy` -- reverse proxy that complements this by enabling subpath discovery in the Vault UI
- ci-docs: `how-tos/adding-a-new-secret-to-ci.md`

## Description

A webservice that allows to manage secret collections in Vault. A secret collection is
a named kv store path in Vault to which members of the secret collection have access.

Authentication is assumed to be delegated to [oauth2 proxy](https://github.com/oauth2-proxy/oauth2-proxy)
and user identity is inferred from the `X-Forwarded-Email` header. The domain portion is stripped.

How does it work? The secret collection manager has CRUD endpoints for managing a secret collection. A
secret collection consists of a group and a policy. The group is needed because policies are an attribute
of either a group or a user. Assigning them to users directly would make secret collection membership
lookups very expensive, as we would need to list all users.

Usernames are expected to come from an alias. Because vault internally uses IDs and not names, the

* GroupName <-> GroupID
* AliasName <-> UserID

mappings are cached after first lookup and assumed to be immutable.

The names of created policies and groups is prefixed with `secret-collection-manager-managed-`. All secret collections
are below a configurable prefix (default: `secret/self-managed`).

Endpoints:
* `GET /secretcollection`: Returns a list of all secret collections for the current user
* `PUT /secretcollection/:name`: Creates a new secret collection using the provided `name`. The secret collection must not exist yet.
* `PUT /secretcollection/:name/members`: Changes the members of an existing secret colltion. The requesting user must be a member of the collection.

## Get the members of a collection's group

* Login to Vault and click the `Access` tab.
* On the `Groups` tab, `Lookup by name` and input "secret-collection-manager-managed-\<collection-name\>"
* Click `Edit` in the returned group and find the list of "Member Identity IDs"
* On the `Entities` tab, `Lookup by id` will return the entity.

## Development

* Use `docker-compose` to start, vault, an oauth2 proxy and a dex instance as an IDP: `cd cmd/vault-secret-collection-manager && docker-compose up`
* If you change the typescript, you need to recompile it via `make cmd/vault-secret-collection-manager/index.js`
* Run the secret-collection-manager via `go run ./cmd/vault-secret-collection-manager  -vault-token=jpuxZFWWFW7vM882GGX2aWOE`
* Visit http://127.0.0.1:4180 and login via `admin` and `password`
