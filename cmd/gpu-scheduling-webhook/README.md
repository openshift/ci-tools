# gpu-scheduling-webhook

## Motivation
Our clusters host some nodes that feature an Nvidia GPU. They are expensive to run workload on so
by using this mutating webhook we ensure that only the pods requesting a GPU actually run on those
nodes, leaving out everything else.

## How it works
A node that features an Nvida GPU holds the following taint:

```yaml
taints:
- effect: NoSchedule
  key: nvidia.com/gpu
  value: "true"
```

The webhook inspects a pod's container requests, both form the init containers and regular ones, and apply
this toleration:

```yaml
tolerations:
- key: nvidia.com/gpu
  operator: Equal
  value: "true"
  effect: NoSchedule
```

when it finds either such a request:

```yaml
requests:
  nvidia.com/gpu: <SOME_VALUE_HERE>
```

or the following limit:

```yaml
limits:
  nvidia.com/gpu: <SOME_VALUE_HERE>
```
