# ci-secret-mirroring-controller

This tool implements a Kubernetes controller that mirrors secrets from one location in the cluster to another in a set of
clusters, either across namespaces or to another name within one namespace. The intent is to allow for users to edit secret values by proxy, without
requiring RBAC privileges on secrets in a central namespace.

The controller is configured to mirror a specific set of secrets and will re-load configuration if it detects changes. An example
configuration can look like:

```yaml
secrets:
- from:
    namespace: source-namespace
    name: dev-secret
  to:
    namespace: target-namespace
    name: prod-secret
```

In order to ensure the integrity of the target secrets, the controller will only update the target secret if a creation or update
is observed on the source secret, and the source secret has a non-zero data field. Not honoring zero-size secret updates or secret
deletion prevents the most common outage scenarios.
