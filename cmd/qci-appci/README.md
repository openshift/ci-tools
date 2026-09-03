# qci-appci

## What
TLS-terminating reverse proxy that fronts `quay.io/openshift/ci` and authenticates users via OpenShift cluster tokens. This allows CI workloads and developers authenticated on the app.ci cluster to pull images from Quay without needing separate Quay credentials. The proxy translates OCP bearer tokens into Quay robot account credentials transparently.

## How it works -- full flow

### Authentication flow
1. Client sends a Docker registry auth request: `GET /v2/auth` with Basic auth (username = anything, password = OCP token)
2. The proxy validates the OCP token via a `TokenReview` against the app.ci cluster
3. For human users (not service accounts), it additionally performs a `SubjectAccessReview` to check `get` permission on `imagestreams/layers` in the `ocp` namespace
4. If valid, the proxy generates a short-lived JWT token (signed with `--token-secret-file`) and returns it
5. Client uses this JWT for subsequent pull requests

### Proxying flow
1. Client sends a pull request (e.g., `GET /v2/openshift/ci/manifests/...`) with `Authorization: Bearer <JWT>`
2. The proxy validates the JWT
3. If valid, replaces the JWT with the Quay robot account's bearer token and forwards the request to `quay.io`
4. Returns the response from Quay to the client

### Robot token maintenance
A background goroutine (`robotTokenMaintainer`) runs every `--interval` (default 30s):
1. Checks if the current Quay robot token is still valid by hitting `GET https://quay.io/v2`
2. If expired or invalid, renews it by authenticating with robot username/password against `https://quay.io/v2/auth?service=quay.io&scope=repository:openshift/ci:pull`
3. Uses exponential backoff (3 retries) on failures

### Token types
- **Cluster token**: an OCP bearer token from the app.ci cluster, validated via TokenReview + SubjectAccessReview
- **App token (JWT)**: a short-lived HMAC-signed JWT issued by this proxy, containing the authenticated user's ID and expiry
- **Robot token**: a Quay.io bearer token obtained using the robot account credentials, used for actual Quay API calls

### Special cases
- The Quay robot account itself can authenticate directly (username/password checked against the robot credentials)
- Service accounts (`system:serviceaccount:*`) bypass the SubjectAccessReview check -- only TokenReview is needed
- Health check endpoint: `GET /healthz` returns 200 OK

### Request logging
All requests are logged with method, URI, status code, response size, duration, and whether a bearer token was present.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--listen-addr` | `127.0.0.1:8400` | Address to listen on |
| `--exposed-host` | `quay-proxy.ci.openshift.org` | Hostname used in `Www-Authenticate` headers |
| `--gracePeriod` | `10s` | Graceful shutdown duration |
| `--robot-username-file` | (required) | Path to the Quay robot username file |
| `--robot-password-file` | (required) | Path to the Quay robot password file |
| `--token-secret-file` | (required) | Path to the HMAC secret for signing JWTs |
| `--token-validity` | `21600s` (6h) | How long issued JWTs are valid |
| `--tls-cert-file` | (required) | Path to the TLS certificate |
| `--tls-key-file` | (required) | Path to the TLS private key |
| `--interval` | `30s` | How often to refresh the Quay robot bearer token |

## Key files
- `cmd/qci-appci/main.go` -- entry point, reverse proxy setup, token services (robot, cluster, app), auth handlers, request routing

## Deployment
Long-lived Deployment on app.ci, exposed via a Route. Serves TLS directly. Requires in-cluster access for TokenReview and SubjectAccessReview API calls.

The external hostname (`quay-proxy.ci.openshift.org`) must be configured in the OCP cluster as an additional image registry so that `oc image` and container runtimes can pull from it.
## Name origin and migration context

The name `qci-appci` comes from this tool being a reverse proxy of the image repository `quay.io/openshift/ci` to which
all images used by tests in the integrated registry of the CI cluster `app.ci` are being mirrored.
The proxy is developed in the context of migrating CI registry from `app.ci` to `quay.io` and
works as the _face_ of CI registry for human users and for some cases, a component running in the CI infrastructure,
e.g., a container on a CI build-farm, referring an image that is promoted during CI.

## Functionality

- The human users from `app.ci` can pull the images in the repo `quay.io/openshift/ci`:

```console
$ podman login -u=$(oc --context app.ci whoami) -p=$(oc --context app.ci whoami -t) quay-proxy.ci.openshift.org --authfile /tmp/t.c
$ podman pull quay-proxy.ci.openshift.org/openshift:ci_ci-operator_latest --authfile /tmp/t.c
```

where `ci_ci-operator_latest` stands for the image stream tag `ci-operator:latest` in the `ci` namespace.

- The robot from the `openshift` org can too. This robot provides the read-only access to the repo.
More details about this comes later.

## How the authentication of `podman` works

[This artical](https://access.redhat.com/solutions/3625131) illustrates
how a client is authenticated against quay.io. More verbose output of `podmand` with `--log-level=trace` below shows that `podman` makes a similar process.

```console
$ podman login -u=$(oc --context app.ci whoami) -p=$(oc --context app.ci whoami -t) quay-proxy.ci.openshift.org --authfile /tmp/t.c --log-level=trace
...
DEBU[0000] GET https://quay-proxy.ci.openshift.org/v2/
DEBU[0000] Ping https://quay-proxy.ci.openshift.org/v2/ status 401
DEBU[0000] GET https://quay-proxy.ci.openshift.org/v2/auth?account=<username>&service=quay-proxy.ci.openshift.org
DEBU[0000] Increasing token expiration to: 60 seconds
DEBU[0000] GET https://quay-proxy.ci.openshift.org/v2/
DEBU[0000] Stored credentials for quay-proxy.ci.openshift.org in credential helper containers-auth.json
Login Succeeded!
DEBU[0000] Called login.PersistentPostRunE(podman login -u=<username> -p=sha256~<secret> quay-proxy.ci.openshift.org --authfile /tmp/t.c --log-level=trace)
DEBU[0000] Shutting down engines
```

In the first response (`status 401`), `quay.io` tells the client the value of `service` as a parameter in the URL for the authentication via with the header `www-authenticate` and the URL `https://quay-proxy.ci.openshift.org/v2/auth` for the authentication. This process can be simulated by the following `curl` cmd: 


```console
$ curl -v https://quay-proxy.ci.openshift.org/v2/
...
> GET /v2/ HTTP/2
> Host: quay-proxy.ci.openshift.org
>
< HTTP/2 401
< www-authenticate: Bearer realm="https://quay-proxy.ci.openshift.org/v2/auth",service="quay-proxy.ci.openshift.org"
...
Unauthorized
* Connection #0 to host quay-proxy.ci.openshift.org left intact

```

Then, `podman` did a basic auth as it was instructed. The bearer token is returned from the server in the body. The second attempt to access `/v2` was done with the bearer token and this time, it passed as expected. The bearer token is used for authorization to access any other endpoint to `quay.io`.

## How `qci-appci` works 
The proxy manipulates the above process:
`app.ci` maintains a valid token to QCI with the provided robot's username and password.

If a request comes to `/v2/auth` for authentication: a generated token will be returned if the one of the following condition is satisfied: 
- It has the robot's username and password,
- The password is a valid token for `app.ci` and if the username represents a human user, it has the authorization to `get` the `imagestreams/layers` in `ocp`, i.e., the token can be used to pull the images in `ocp`.

Otherwise, the request will be denied with `401`.

The generated token is a [JWT token](https://jwt.io/) signed by a secret provided to `qci-appci`. Any request to `qci-appci` other than path `/v2/auth` will require a valid JWT token. Otherwise, the request gets `401`. `qci-appci` replaces the valid token in the request with the QCI token and forwards the request to `quay.io` and forwards the response from `quay.io` to the client.

## Authorization for human users

For human users, `qci-app.ci` authorizes the requests with token that can pull images from `ocp` on `app.ci`. [Our document](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#human-users) tells our users to bind their group to the role `qci-image-puller`. In reality, this condition becomes unnecessary as `ocp` allows all authenticaed users to pull its images by the following `RoleBinding`:  

```console
$ oc get rolebinding -n ocp image-puller -o yaml | yq -y '.subjects[0]'
apiGroup: rbac.authorization.k8s.io
kind: Group
name: system:authenticated
```

The users that follow the documentation above do not rely on the above `RoleBinding`, i.e., they would still be able to pull images from QCI even if the above `RoleBinding` is modified or deleted.
