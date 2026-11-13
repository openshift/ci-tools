# GSM proxy

`gsm-proxy` lets the secret-manager CLI list secret names and describe secret metadata in
Google Secret Manager collections owned by the user's Rover groups.

## Access

Users authenticate through Red Hat SSO via Dex. Requests carry a Dex bearer token for the
`gsm-cli` client. Collection ownership is defined in the release repository's
[`core-services/sync-rover-groups/_config.yaml`](https://github.com/openshift/release/blob/main/core-services/sync-rover-groups/_config.yaml)
and reloaded when it changes. The proxy uses its own Google service account to access GSM
and logs requests for auditing.

## Authentication flow

```mermaid
sequenceDiagram
    participant CLI as Secret-manager CLI
    participant Browser
    participant Dex as Dex (idp.ci.openshift.org)
    participant SSO as Red Hat SSO
    participant Proxy as GSM proxy
    participant GSM as Google Secret Manager

    Note over CLI,SSO: Login (planned CLI integration)
    CLI->>Browser: Open authorization URL with state and PKCE S256 challenge
    Browser->>Dex: Authorize client gsm-cli
    Dex-->>Browser: Redirect to Red Hat SSO
    Browser->>SSO: Authenticate
    SSO-->>Browser: Redirect back to Dex
    Browser->>Dex: Complete upstream callback
    Dex->>SSO: Obtain identity and Rover groups
    Dex-->>Browser: Redirect to loopback callback with code and state
    Browser->>CLI: http://127.0.0.1:8085/callback
    CLI->>CLI: Validate state
    CLI->>Dex: Exchange code with PKCE verifier and registered client credentials
    Dex-->>CLI: Signed ID token (username and groups)

    Note over CLI,GSM: Collection listing (implemented proxy API)
    CLI->>Proxy: GET /api/v1/collections/{collection}/secrets<br/>Authorization: Bearer ID_TOKEN
    Proxy->>Proxy: Validate collection name
    opt Signing keys need fetching (rate limited)
        Proxy->>Dex: GET /keys
        Dex-->>Proxy: Public signing keys
    end
    Proxy->>Proxy: Verify signature, issuer, sole audience, expiry and identity claims
    alt Missing or invalid token
        Proxy-->>CLI: 401 Unauthorized
    else Valid token
        Proxy->>Proxy: Check token groups against loaded ownership config
        alt No claimed group owns the collection
            Proxy-->>CLI: 403 Forbidden
        else Authorized
            Proxy->>GSM: ListSecrets using proxy service account (all matching pages)
            GSM-->>Proxy: Secret metadata
            Proxy->>Proxy: Extract names only, enforce exact collection match,<br/>exclude internal secrets and sort paths
            Proxy-->>CLI: 200 JSON secrets array
        end
    end
```

## Endpoints

- `GET /api/v1/collections/{collection}/secrets`: lists the collection's secret paths.
- `GET /api/v1/collections/{collection}/secrets/{path...}`: describes a secret's metadata.
- `GET /healthz`: health check, no authentication required.

## Deployment

The [deployment manifests](https://github.com/openshift/release/blob/main/clusters/core-ci/gsm-proxy/manifests.yaml)
configure two replicas in the `ci` namespace on `core-ci`, exposed at
`https://gsm-proxy.ci.openshift.org` through a reencrypt ingress.

The pods mount the ownership ConfigMap, Google service-account credentials, and a TLS serving
certificate. The Google service account needs only `secretmanager.secrets.list` and
`secretmanager.secrets.get`; it must not be able to read secret payloads
(`secretmanager.versions.access`) or modify secrets.

## Known limitations

- Ownership reloads do not revoke group membership in already-issued tokens; a removed user keeps
  access until their token expires (at most one hour).
- GSM calls rely on the Google client's default 60-second per-call timeout; there is no proxy-side
  request deadline.

## Running locally

From the repository root, with a release checkout and Google service-account credentials:

```bash
go run ./cmd/gsm-proxy \
  --groups-ownership-file=/path/to/release/core-services/sync-rover-groups/_config.yaml \
  --gsm-project-id=openshift-ci-secrets \
  --gsm-credentials-file=/path/to/gsm-proxy-service-account.json
```

The default listener is `127.0.0.1:8080`. Non-loopback listeners require `--tls-cert-file` and
`--tls-key-file`. Use `--help` for all options.

Run tests with `go test ./cmd/gsm-proxy ./pkg/gsm-proxy`.
