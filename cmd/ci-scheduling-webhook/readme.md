
# Why do we need this?

## Workload Segmentation
We have several types of workloads: builds, short running tests, long running tests, and prowjobs. Builds are intense and short-lived. Most tests are longer-running than builds and less intensive. Even longer tests can run hours. Prowjobs must run the summation of the length of time of suborinates tests.

In short, these things run different amounts of time. We want to segment these workloads for multiple reasons.

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
3. That OSD won't let us cordon for longer than 10 minutes without raising alerts.
4. That the autoscaler can't figure out complex scheduling patterns (custom taints, anti-pod affinity, ...).
5. The huge influxes of pods common to our testing infrastructure.
6. It has to be simple, reliable, and effective. 

# Design

## Workload classes
Workload class: tests, builds, longtests, prowjobs. Each class has its own machineset & autoscaler. Each machineset creates nodes with taints & labels. As pods are created, the webhook will classify them and, by applying a runtimeclass to them, ensure that they only land on nodes created by their classes' machineset.

## The cluster autoscaler scales up
The autoscaler scales up machinesets when there are unschedulable / Pending pods that match the respective machineset class. This is its normal behavior and we rely on it.

## The webhook scales down
The webhook will modify nodes in each classed machineset to ensure the autoscaler does not try to scale them down. The webhook is solely in charge of scale downs. The out-of-the-box autoscaler is very slow to reclaim resources, so we do it ourselves.

## Avoidance states
As the system evolves, the webhook tries to steer workloads away from nodes it wants to reclaim. Generally, it will be trying to claim ceil(25%) of nodes in the class at any given moment. It does this with avoidance states. The states are None (no avoidance of node), PreferNoSchedule (implemented as a PreferNoSchedule taint effect), and NoSchedule (implemented with cordon). 

There is a periodic evaluation loop running for each node class. During each evaluation loop, the webhook will at least want to set 25% of the class' nodes to PreferNoSchedule. However, if it finds that a node has zero running pods associated with the workload class (e.g. ignoring daemonsets), it will set NoSchedule (cordon the node). If the loop runs again and finds a node cordoned and still running zero classed pods, it will trigger a scale down of that node.

## Pod Node Affinity
To keep focus on scaling down nodes (PreferNoSchedule is not perfect), incoming pods are also given a node to preclude (this means their nodeAffinity is configured to guarantee it is not scheduled to a specific node). Incoming pods generally always preclude a node if there is more node available in the class. The precluded node is the first node selected by the node avoidance ceil(25%) algorithm (i.e. the most likely to scale down next). This ensure there is always pressure on the system to try to reclaim a node. 

# Deploying
1. Create one machineset and machineautoscaler per class. Unfortunately, these machinesets are cluster  & cloud specific. Model on existing machinesets (take care to include node ci-workload label and taint). Min=1, Max=80 on each autoscaler.
2. Apply cmd/ci-scheduling-webhook/res/admin.yaml .
3. Apply cmd/ci-scheduling-webhook/res/rbac.yaml .
4. Apply cmd/ci-scheduling-webhook/res/deployment.yaml .
5. Apply cmd/ci-scheduling-webhook/res/dns.yaml . This makes sure DNS can schedule daemonset pods to tainted nodes (https://bugzilla.redhat.com/show_bug.cgi?id=2086887).
6. Verify deployment is running in ci-scheduling-webhook.
7. Verify machinesets have scaled up to at least one node.
8. Apply cmd/ci-scheduling-webhook/res/webhook.yaml .

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
[ci-tools]$ go run github.com/openshift/ci-tools/cmd/ci-scheduling-webhook --as system:admin --port 8443
[ci-tools]$ cmd/ci-scheduling-webhook/testing/post-pods.sh
```
