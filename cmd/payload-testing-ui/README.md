payload-testing-ui
==================

`payload-testing-ui` is the visualization web server for the
`PullRequestPayloadQualificationRun` `CustomResource`.  Its purpose is to be
linked from pull requests in Github from which qualification runs are created to
display information about them.

Testing
-------

The server only requires a read-only `kubeconfig` targeting a cluster where the
`CustomResource` objects are configured.  Only `list`, `get`, and `watch`
permissions are required (the UI is entirely read-only).  The production DPTP
deployment lives in [`app.ci`][deployment] and uses a service account with only
those permissions.

If no changes to the CRD are necessary, the easiest local setup is to create a
`kubeconfig` for that same service account targeting the same cluster:

```console
$ cat > kubeconfig.yaml <<EOF
apiVersion: v1
kind: Config
current-context: app.ci
clusters:
- cluster:
    server: https://api.ci.l2s4.p1.openshiftapps.com:6443
  name: api-ci-l2s4-p1-openshiftapps-com:6443
contexts:
- context:
    cluster: api-ci-l2s4-p1-openshiftapps-com:6443
    user: payload-testing-ui/api-ci-l2s4-p1-openshiftapps-com:6443
  name: app.ci
users:
- name: payload-testing-ui/api-ci-l2s4-p1-openshiftapps-com:6443
  user:
    token: $(oc \
        --context app.ci --as system:admin --namespace ci \
        create token --duration 720h payload-testing-ui)
EOF
```

The server uses the standard loading method (`KUBECONFIG` followed by in-cluster
credentials), so to start the server from a locally-built executable using these
credentials, do:

```console
$ KUBECONFIG=kubeconfig.yaml payload-testing-ui --port 8000
```

[deployment]: https://github.com/openshift/release/tree/main/clusters/app.ci/payload-testing-ui
