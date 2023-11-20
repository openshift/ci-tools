# qci-appci

The name `qci-appci` comes from this tool being a reverse proxy of the image repository `quay.io/openshift/ci` to which
all images used by tests in the integrated registry of the CI cluster `app.ci` are being mirrored.
The proxy is developed in the context of migrating CI registry from `app.ci` to `quay.io` and
works as the _face_ of CI registry for human users and for some cases, a component running in the CI infrastructure,
e.g., a container on a CI build-farm, referring an image that is promoted during CI.

# Functionality

- The human users from `app.ci` can pull the images in the repo `quay.io/openshift/ci`:

```console
$ podman login -u=$(oc --context app.ci whoami) -p=$(oc --context app.ci whoami -t) quay-proxy.ci.openshift.org --authfile /tmp/t.c
$ podman pull quay-proxy.ci.openshift.org/openshift:ci_ci-operator_latest --authfile /tmp/t.c
```

where `ci_ci-operator_latest` stands for the image stream tag `ci-operator:latest` in the `ci` namespace.

- The robot from the `openshift` org can too. This robot provides the read-only access to the repo.
More details about this comes later.

# How it works

[This artical](https://access.redhat.com/solutions/3625131) illustrates
how a client is authenticated against quay.io. More verbose output of `podmand` with `--log-level=trace` below
shows that `podman` makes a similar process.

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

In the first response (`status 401`), `quay.io` tells the client the value of `service` as a parameter in the URL
for the authentication via with the header `www-authenticate` and the URL `https://quay-proxy.ci.openshift.org/v2/auth` for the authentication.
Then `podman` did a basic auth as it was instructed. The bearer token is returned from
the server in the body.
The second attempt to access `/v2` was done with the bearer and this time, it passed as expected.

```console
$ url -v https://quay-proxy.ci.openshift.org/v2/
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

The proxy manipulates the above process:

- It passes over the request to `quay.io` if robot's credentials are found in the request.
- It returns `401` when a client sends a request to the server without any token and the path is not `v2/auth`.
  Otherwise it checks if the bearer token from the request can be used to log into `app.ci` and returns  `401`
  upon an authentication failure. In case of success, it returns _the token from the request_ to the client.
- When a client accesses to any other endpoint, the token will be verified again and replace
  by a token from the robot. In the background, the token of the robot's account us is maintained periodically.