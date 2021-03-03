# Vault secret collection manager

A webservice that allows to manage secret collections in Vault. A secret collection is
a named kv store path in Vault to which members of the secret collection have access.

Authentication is assumed to be delegated to [oauth2 proxy](https://github.com/oauth2-proxy/oauth2-proxy)
and user identity is inferred from the `X-Forwarded-Email` header. The domain portion is stripped.

How does it work? The secret colletion manager has CRUD endpoints for managing a secret collection. A
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
* `PATCH /secretcollection/:name`: Changes the members of an existing secret colltion. The requesting user must be a member of the collection.
