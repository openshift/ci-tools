# HyperShift Namespace Reconciler

This controller is responsible for making sure the namespaces created by HyperShift
are ignored by both cluster-monitoring and user-workload monitoring by manipulating
the labels on the namespaces on the `hive` cluster:

- `openshift.io/cluster-monitoring: "true"` does not exist
- `openshift.io/user-monitoring: "false"` exists

This controller is
a [workaround](https://redhat-internal.slack.com/archives/C01C8502FMM/p1672850968426149?thread_ts=1672735666.352179&cid=C01C8502FMM)
before [HOSTEDCP-706](https://issues.redhat.com/browse/HOSTEDCP-706) is implemented.
