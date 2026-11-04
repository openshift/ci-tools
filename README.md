# CI-Tools

This repositotory contains tooling used in Openshift CI. Please refer to the
[documentation](https://docs.ci.openshift.org/) for details.

## Multi-stage post-step startup recovery

The multi-stage runner retries a post-step pod at most once after a pending
timeout with a matching kubelet `FailedCreatePodSandBox` event reporting
`context deadline exceeded`. It requires complete never-started status for every
regular and init container, a scheduled node, and a missing sandbox. Missing or
ambiguous status, any previous execution, image/configuration failures, API
errors, cancellation, and explicitly node-pinned pods do not qualify. Pre/test
steps and observers are not retried.
An established pod watch is required; a timeout before watch establishment is
treated as unknown infrastructure state rather than permission to retry.
Execution evidence observed earlier in the attempt also prevents replay even if
later status no longer reports it.
Pods found already existing during creation do not qualify, even if the creation
helper subsequently replaces a completed pod; their earlier history is not
trusted for replay.

Before replacement, the runner conditionally deletes the observed pod using its
UID and resource version and confirms deletion. The new attempt excludes the
failed node while retaining existing scheduling constraints. Attempt manifests,
startup events in the failure output, and a failed startup-attempt JUnit case
remain available even when recovery succeeds. This is not general step replay
or a guarantee of cleanup during an infrastructure outage.
If deletion changes the pod to a terminal phase or reports
`ContainerStatusUnknown`, recovery is suppressed even when execution fields are
empty. Missing runtime history is not proof that a workload never ran. The
runner requires positive never-started status until the original pod disappears.
If conditional deletion conflicts, replay is suppressed. Cleanup refreshes the
pod and deletes only the original UID, then waits for its disappearance before
continuing. A different pod with the same name is not deleted. These API
observations do not guarantee exactly-once workload execution.
