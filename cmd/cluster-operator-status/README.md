# cluster-operator-status

`cluster-operator-status` is a tool that analyzes the health status of OpenShift cluster operators. It can either read cluster operator status from stdin (parsing `oc get co` output) or query a cluster directly.

## Usage

### Reading from stdin

Parse cluster operator status from `oc get co` output:

```bash
oc --context build01 get co | cluster-operator-status --from-stdin
```

### Querying a cluster directly

Query cluster operators from a specific context:

```bash
cluster-operator-status --context build01
```

Or use the default/in-cluster context:

```bash
cluster-operator-status
```

## Output

The tool provides a summary of cluster operator health:

- **Total Operators**: Count of all cluster operators
- **Healthy**: Operators that are available, not degraded, and not progressing
- **Degraded**: Operators with `Degraded=True`
- **Unavailable**: Operators with `Available=False`
- **Progressing**: Operators that are currently updating (normal during upgrades)

For each problematic operator, it displays:
- Operator name
- Version
- Time since the status changed
- Error/degradation message

## Examples

### Analyze from command output

```bash
$ oc --context build01 get co | cluster-operator-status --from-stdin

=== Cluster Operator Health Summary ===

Total Operators: 35
  ✓ Healthy: 28
  ⚠ Degraded: 5
  ✗ Unavailable: 2
  ⟳ Progressing: 7

=== Degraded Operators ===

authentication (Version: 4.19.19, Since: 36d)
  Message: APIServerDeploymentDegraded: 1 of 3 requested instances are unavailable...

=== Unavailable Operators ===

kube-storage-version-migrator (Version: 4.19.19, Since: 14h)
  Message: KubeStorageVersionMigratorAvailable: Waiting for Deployment
```

### Query cluster directly

```bash
$ cluster-operator-status --context build01

=== Cluster Operator Health Summary ===
...
```




