# Cluster Pools Pull Secret Provider

This controller is responsible for making sure `secret/pull-secret` exists in each namespace with a cluster pool using the secret in `clusterPool.Spec.PullSecretRef.Name` on the `hive` cluster.
