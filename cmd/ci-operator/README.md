# Profile-free AWS authentication tests

Multi-stage tests that only exercise AWS authentication can set `steps.aws_sts`
instead of `cluster_profile`:

```yaml
steps:
  aws_sts:
    home_role_arn: arn:aws:iam::000000000000:role/ci-step-runner
    hub_role_arn: arn:aws:iam::111111111111:role/ci-step-runner-hub
    target_role_arn: arn:aws:iam::111111111111:role/test-target
  test:
  - as: check-authentication
    from: test-bin
    commands: ./check-authentication
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
```

All three IAM role ARNs are required. The test cannot also use a cluster profile
or a cluster claim. CI projects a short-lived, rotating service-account token
with audience `sts.amazonaws.com` at `/var/run/secrets/aws/sts-token/token` and
mounts an AWS configuration at `/var/run/secrets/aws/config/config`. The AWS
configuration defines `home`, `hub`, and `default` profiles in that order. Tests
must explicitly use that configuration and disable unintended credential sources.

This does not acquire a cloud quota lease or grant any AWS permissions. The home
role must already trust the selected build cluster's issuer and test service
account, and each role must already permit the next hop. Choose a build cluster
whose issuer the home role trusts. Explicit step leases still apply if configured.
Existing tests using cluster profiles keep their existing behavior. STS-enabled
steps require the normal test service account and cannot set `no_kubeconfig`.
