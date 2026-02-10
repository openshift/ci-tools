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

# How the authentication of `podman` works

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

# How `qci-appci` works 
The proxy manipulates the above process:
`app.ci` maintains a valid token to QCI with the provided robot's username and password.

If a request comes to `/v2/auth` for authentication: a generated token will be returned if the one of the following condition is satisfied: 
- It has the robot's username and password,
- The password is a valid token for `app.ci` and if the username represents a human user, it has the authorization to `get` the `imagestreams/layers` in `ocp`, i.e., the token can be used to pull the images in `ocp`.

Otherwise, the request will be denied with `401`.

The generated token is a [JWT token](https://jwt.io/) signed by a secret provided to `qci-appci`. Any request to `qci-appci` other than path `/v2/auth` will require a valid JWT token. Otherwise, the request gets `401`. `qci-appci` replaces the valid token in the request with the QCI token and forwards the request to `quay.io` and forwards the response from `quay.io` to the client.

# Authorization for human users

For human users, `qci-app.ci` authorizes the requests with token that can pull images from `ocp` on `app.ci`. [Our document](https://docs.ci.openshift.org/how-tos/use-registries-in-build-farm/#human-users) tells our users to bind their group to the role `qci-image-puller`. In reality, this condition becomes unnecessary as `ocp` allows all authenticaed users to pull its images by the following `RoleBinding`:  

```console
$ oc get rolebinding -n ocp image-puller -o yaml | yq -y '.subjects[0]'
apiGroup: rbac.authorization.k8s.io
kind: Group
name: system:authenticated
```

The users that follow the documentation above do not rely on the above `RoleBinding`, i.e., they would still be able to pull images from QCI even if the above `RoleBinding` is modified or deleted.
