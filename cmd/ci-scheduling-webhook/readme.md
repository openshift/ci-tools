# Why do we need this?

## Workload Segmentation
We have two different types of workloads: builds and tests. Builds are intense and short-lived. Tests are long-running and less intensive. We want to segment these workloads for multiple reasons.

### KubeletConfig Container Runtime Reserved Resources
Builds drive significant overhead on the container runtime that is not accounted for by the pod-autoscaler. Historically, we've tried to accommodate that with hope(tm) and reserved system CPU for the kubelet via `kind: KubeletConfig`. Problems with this include:

a. OSD does not allow us to use our own `KubeletConfig`.

b. `KubeletConfig`'s cannot be applied deterministically applied (https://bugzilla.redhat.com/show_bug.cgi?id=2068792).

c. `KubeletConfig`'s They cannot be sized dynamically based on the node (https://bugzilla.redhat.com/show_bug.cgi?id=2068795). 

d. `KubeletConfig`'s don't work with the autoscaler without hacks, at present (https://bugzilla.redhat.com/show_bug.cgi?id=2068336).

To overcome this, the webhook segments workloads by labeling them and adding tolerations based on whether they are build or test workloads. The workloads land on differently labeled nodes which they have their own `kind: RuntimeClass` . The `RuntimeClass` adds `overhead` CPU&Memory when a pod is scheduled to a class of node. As such test and build pods get different overheads assigned automatically by kubernetes as they are admitted. At present, this means we give builds an entire extra CPU core to themselves to help ensure sufficient capacity for the container runtime. 

Without sufficient CPU, the container runtime will start reporting errors like "context deadline exceeded" and builds are likely to fail.

### Different IOP Characteristics 
Tests and builds have different IOP requirements for their nodes. Segmenting the workloads allows us to tune these values independently to reduce costs.

### Different Instance Types
Segmentation allows us to experiment with different machines types for different workloads to help reduce costs.

### Analysis
Segmenting the workloads onto different nodes allows us to more effectively analyze them. Consider trying to find the appropriate IOPS setting for our nodes if you could not predict the type of workload they would receive.

## Kubernetes Scheduling (the nightmare of) 
By default, kubernetes uses a "least allocated" scheduling scoring that tries to spread workloads out across extant nodes. This is great if you have a fixed set of nodes you want to utilize without overloading them. However, when a horizontal autoscaler is involved, this is decidedly *not* what we want. Our cluster usage is pathological:

1. A large wave of pods will hit be created on a cluster. 
2. The cluster will scale up. 
3. Thereafter, a small number of pods will be scheduled across those nodes.

The OpenShift machineautoscaler would normally be able to figure this out and evict pods that were keeping under-utilized nodes from being scaled down. The autoscaler will only evict pods under certain circumstances -- most importantly for us, individual pods are only considered if they are part of a replicaset (i.e. certain to be rescheduled if evicted).

Because our workloads are not created by replicasets, the autoscaler considers them unevictable (ignoring the fact that they have PDBs that would also prevent it). Thus, a single workload on each node is enough to keep it alive. Before this webhook, this meant that we kept systems alive for much longer than necessary -- until there were so few pods entering the system that the scheduling couldn't keep the scaled up nodes occupied any longer. 

So you say, why not turn off this "least allocated" behavior. That is an option if you create your own scheduler, which k8s makes pretty trivial. There is a scorer that schedules pods on the "most allocated" nodes. *But* if you do this, you hit a problem: until k8s 1.23, which we don't run, the scheduler and kubelet can disagree on what resources are available on a node: https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672 . Ultimately, the more we pack nodes, the more jobs will fail because of errors like "OutOfCpu" until this is fixed. 

For what it is worth, another problem was that did not have enough system reserved CPU for builds if we packed them too tightly (see KubeletConfig rant above). However, I believe we can improve this behavior now with the introduction of RuntimeClass resources.

Why not try (insert clever solution here)? Well you have to contend with:
1. https://github.com/kubernetes/kubernetes/issues/106884#issuecomment-1005074672
2. The fact that the autoscaler will only scale up if it can't fit unschedulable pods into existing nodes (unless those nodes are cordoned).
3. That OSD won't let us cordon.
4. That the autoscaler can't figure out complex scheduling patterns (taints, anti-pod affinity, ...).
5. The huge influxes of pods common to our testing infrastructure.
6. It has to be simple, safe, and effective. 

The idea implemented in the webhook has two aspects:
Aspect 1:
1. Scan nodes on the cluster and taint any that have 1 or 0 pods as PreferredNoSchedule.
2. If the cluster is not under load, these taints will help ensure the autoscaler will scale these nodes down.
3. If new pods are scheduled onto this PreferredNoSchedule nodes, the taint is removed. This indicates the cluster is under pressure. 

Aspect 2:
1. Scan nodes and find 10% of nodes in the cluster that look close to being able to be scaled down (assessed by low pod count).
2. Begin modifying all incoming pods to have anti-node affinity for those "sacrificial nodes".
3. Preventing new pods from being scheduled on these nodes means they stand in improving chance of being selected for scale down once their final pod(s) terminate gracefully.
4. Keep using anti-affinity on these nodes until they disappear from the cluster (by the autoscaler scaling them down).
5. As nodes disappear, keep 10% of the nodes in the cluster as sacrificial nodes.

This provides a gentle pressure to the cluster to allows nodes to be scaled down over time. The only known downsides to this approach are:
1. It slowly drives up utilization of nodes, which adds full to the OutOfCpu (but not nearly as much as using the "most allocated" scoring).
2. It means that, even if the cluster is scaling up, a subset of nodes (the sacrificial ones) will be considered unschedulable by the system. The webhook tries to reduce the cost of this by detecting when the node count is reaching its maximum size and limiting the sacrificial nodes to 1, once it is reaching that level. In short, if the cluster is actually being maxed out, the webhook tries to step back.

# Deploying
1. Create two new machinesets and machineautoscalers. One for "tests" and one for "builds". Unfortunately, these machinesets are cluster  & cloud specific. Model on existing machinesets (take care to include node ci-workload label and taint). Min=1, Max=80 on each autoscaler.
2. Apply cmd/ci-scheduling-webhook/res/admin.yaml .
3. Apply cmd/ci-scheduling-webhook/res/rbac.yaml .
4. Apply cmd/ci-scheduling-webhook/res/deployment.yaml .
5. Verify deployment is running in ci-scheduling-webhook.
6. Verify machinesets have scaled up to at least one node.
7. Apply cmd/ci-scheduling-webhook/res/webhook.yaml .

# Hack

## Manual Image Builds 
```shell
[ci-tools]$ CGO_ENABLED=0 go build -ldflags="-extldflags=-static" github.com/openshift/ci-tools/cmd/ci-scheduling-webhook
[ci-tools]$ sudo docker build -t quay.io/jupierce/ci-scheduling-webhook:latest -f images/ci-scheduling-webhook/Dockerfile .
# Temporary hosting location
[ci-tools]$ sudo docker push quay.io/jupierce/ci-scheduling-webhook:latest
```

## Local Test
```shell
[ci-tools]$ export KUBECONFIG=~/.kube/config
[ci-tools]$ go run github.com/openshift/ci-tools/cmd/ci-scheduling-webhook --as system:admin --port 8443 --shrink-cpu-requests-tests 0.3 &
[ci-tools]$ cmd/ci-scheduling-webhook/testing/post-pods.sh
```

## Manual Deployment
```shell
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/admin.yaml
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/deployment.yaml
# Check deployment is running
[ci-tools]$ oc --as system:admin apply -f ./cmd/ci-scheduling-webhook/res/webhook.yaml
```
