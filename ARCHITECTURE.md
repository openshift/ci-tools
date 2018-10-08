# CI Operator Architecture

The CI Operator has a number of foundational principles:

 - don't do work twice
 - do the least amount of work needed
 - hide complexity from the end user

To achieve this, the CI Operator requires configuration to understand the build
process for each component as well as every output container image. This
document overviews the workflow that the CI Operator uses to build components
and structure tests.

Every invocation of `ci-operator` creates a workspace to isolate test execution,
seed it with build inputs and the published component images from the OpenShift
release if the component under test is a part of one, then schedule test
workflows as Kubernetes and OpenShift objects. This workflow is executed using
the following steps:

 1. input resolution
 1. namespace initialization
 1. build graph traversal

The following sections delve into more detail for all of these steps. For clarity,
an invocation of `ci-operator` to build a specific test target will be called a
job; a specific instance of such an invocation is a build of the job.

This overview uses the output of build #30 of the `pull-ci-openshift-machine-config-operator-master-images`
job to illustrate the workflow. See the published artifacts for this job [here](https://openshift-gce-devel.appspot.com/build/origin-ci-test/pr-logs/pull/openshift_machine-config-operator/112/pull-ci-openshift-machine-config-operator-master-images/30?log#log).

## Input Resolution

To avoid repeating work, `ci-operator` needs to determine when work can be
re-used. The tool identifies a build of any specific job with a hash of:

 - job metadata (refs to test, clone configuration)
 - configuration (YAML configuration for refs under test)
 - other inputs (levels of input tagged images)

With such an identifier, the `ci-operator` can determine if two builds are using
the same configuration on the same inputs and can therefore re-use common work.
This identifier is used to create the Kubernetes `Namespace` in which the test
workloads will run and is furthermore available to templatized tests with the
[`$NAMESPACE`](./TEMPLATES.md#namespace) variable.

Input resolution can be identified in the `ci-operator` output by all of the
steps that precede the creation of the test `Namespace`:

```
2018/10/08 05:28:45 Resolved source https://github.com/openshift/machine-config-operator to master@dc1d2c7f, merging: #112 f63d7a2c @flaper87
2018/10/08 05:28:45 Resolved openshift/release:golang-1.10 to sha256:43ad4740f25e4cbc6de8a964e2ce65ce50fbc24fc4e5e268e0a7370ca3b09bd1
2018/10/08 05:28:45 Resolved openshift/origin-v4.0:base to sha256:903622079f42a79fd6f3bbb6f2419292883b94c3227638fc93a99f77ebc96b00
2018/10/08 05:28:45 Using namespace ci-op-31xmgx1s
```

## Namespace Initialization

The hash created from input resolution is used to create a `Namespace` as an
isolated workspace for the test; the `Namespace` is subsequently initialized for
use by the test workloads.

All input images for the tests that are described in the configuration YAML are
tagged in, as are all images that form the larger release that the test is a part
of. Images that are used for the build graph, like those identified with the
[`base_images`](./CONFIGURATION.md#base_images),
[`base_rpm_images`](./CONFIGURATION.md#base_rpm_images), and
[`build_root`](./CONFIGURATION.md#build_root) stanzas, have `ImageStreamTag`s
created for them in the `pipeline` `ImageStream` in the test `Namespace`. Images
that are part of the release that the test exists within, as specified with the
optional [`tag_specification`](./CONFIGURATION.md#tag_specification) stanza, are
mirrored to `ImageStreamTag`s in the `stable` `ImageStream` within the test
`Namespace`.

In order to ensure that resources from tests do not leak on the cluster the tests
are executed on, both hard and soft TTLs are set on the `Namespace` and the
[`ci-ns-ttl-controller`](https://github.com/openshift/ci-ns-ttl-controller) is
used to enforce the TTLs and reap namespaces when TTLs have expired. Both a hard
and a soft TTL are set on the namespaces; the hard TTL describes how much time
can pass after the creation of the `Namespace` before it is reaped, the soft TTL
described how much time can pass without any active `Pod`s in the `Namespace`
before it is reaped. Whichever TTL is reached first triggers reaping.

Namespace initialization follows input resolution in the `ci-operator` output:

```
2018/10/08 05:28:45 Creating namespace ci-op-31xmgx1s
2018/10/08 05:28:47 Creating rolebinding for user flaper87 in namespace ci-op-31xmgx1s
2018/10/08 05:28:47 Setting a soft TTL of 1h0m0s for the namespace
2018/10/08 05:28:47 Setting a hard TTL of 12h0m0s for the namespace
2018/10/08 05:28:47 Setting up pipeline imagestream for the test
2018/10/08 05:28:47 Tagging openshift/release:golang-1.10 into pipeline:root
2018/10/08 05:28:47 Tagging openshift/origin-v4.0:base into pipeline:base
2018/10/08 05:28:47 Tagged release images from openshift/origin-v4.0:${component}, images will be pullable from registry.svc.ci.openshift.org/ci-op-31xmgx1s/stable:${component}
```

## Build Graph Traversal

A configuration file for the CI Operator defines build steps, test targets and
output images for a component git repository. A graph of build dependencies is
built from this configuration in order to determine what concrete actions need
to occur for any specific target. Each invocation of `ci-operator` specifies one
or more `--target`s to execute; for each target, the build graph is traversed to
execute dependent steps first.

The CI Operator configuration file creates some implicit build steps:

| Output `ImageStreamTag` | Action |
| ----------------------- | ------ |
| `pipeline:src`          | clones the refs under test |
| `pipeline:bin`          | runs the [`binary_build_commands`](./CONFIGURATION.md#binary_build_commands) |
| `pipeline:test-bin`     | runs the [`test_binary_build_commands`](./CONFIGURATION.md#test_binary_build_commands) |
| `pipeline:rpms`         | runs the [`rpm_build_commands`](./CONFIGURATION.md#rpm_build_commands) |

Container image builds -- whether from the implicit `pipeline` steps above or
from explicit image build configurations in the [`images`](./CONFIGURATION.md#images)
stanza, are executed using OpenShift `Build`s. Test targets in the [`tests`](./CONFIGURATION.md#tests)
stanza are executed using Kubernetes `Pod`s. As all of the test workflow execution
objects are created in a `Namespace` shared for all jobs with the same input,
re-use is achieved by deterministic naming. For instance, the `src` `Build` that
creates the `pipeline:src` `ImageStreamTag` will be created only once in a given
`Namespace`; other builds of jobs that require this build step will see the
`Build` running and wait for it to complete or see the `ImageStreamTag` existing
and consider the build step finished.

Build graph traversal follows `Namespace` initialization in the `ci-operator`
output, with parallel execution of steps leading to interwoven log output:

```
2018/10/08 05:28:47 Building src
2018/10/08 05:29:15 Build src succeeded after 29s
2018/10/08 05:29:15 Building machine-config-daemon
2018/10/08 05:29:15 Building machine-config-server
2018/10/08 05:29:15 Building machine-config-operator
2018/10/08 05:29:15 Building machine-config-controller
2018/10/08 05:31:47 Build machine-config-server succeeded after 2m28s
2018/10/08 05:31:47 Tagging machine-config-server into stable
2018/10/08 05:31:56 Build machine-config-operator succeeded after 2m37s
2018/10/08 05:31:56 Tagging machine-config-operator into stable
2018/10/08 05:31:56 Build machine-config-daemon succeeded after 2m37s
2018/10/08 05:31:56 Tagging machine-config-daemon into stable
2018/10/08 05:32:00 Build machine-config-controller succeeded after 2m41s
2018/10/08 05:32:00 Tagging machine-config-controller into stable
2018/10/08 05:32:00 All images ready
```