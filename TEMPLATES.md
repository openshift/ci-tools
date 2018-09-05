# Using `Template`s as Execution Targets With CI Operator

The CI Operator allows for users to configure tests running in a single container
with the `tests` array. In many cases, however, running a command in a single
container will not suffice. For more complicated tests, the CI Operator supports
a `--template` option with which a test author may write black-boxed execution
targets using OpenShift `Template`s. This document explains the interfaces expected
for test `Template`s and best practices for how an author should configure their
`Template`.

The high-level contract for a `Template` execution target is that the `Template`
will be processed into the test's `Namespace` once prerequisite `Build`s have
occurred. Artifacts will be gathered from `artifacts` volumes on `Pod`s in
the `Template`, if any are present. What work occurs in a `Template`'s `Pod`s is
a black box as far as the CI Operator is concerned.

## Parameters Available to `Template`s

A number of parameters are available to the `Template` and will be provided if
the `Template` is explicitly configured to consume them. The following parameters
will be provided to `Template`s:

#### `NAMESPACE`
The namespace in which your `Template` is processed. Target this namespace with
your objects.

| Example: | `ci-op-<input-hash>` |
| - | - |

#### `IMAGE_FORMAT`
The image repository URL template using which your job's release images can
be pulled, formatted for the OpenShift Ansible `-e oreg_url` parameter.
`Template`s depending on this parameter will be processed only after all
images are built.

| Example: | `registry.svc.ci.openshift.org/ci-op-<input-hash>/stable:${component}` |
| - | - |

#### `RPM_REPO`
The RPM repository URL from which your job's RPMs can be installed.
`Template`s depending on this parameter will be processed only after the
`rpm` image is built.

| Example: | `http://rpm-repo-ci-op-<input-hash>.svc.ci.openshift.org` |
| - | - |

#### `IMAGE_<component>`
The image repository URL from which the `<component>` image can be pulled.
If a component name has hyphens, replace them with underscores. For instance,
the `$IMAGE_SOME_NAME` parameter would point to the registry URL for `some-name`.
Valid `<component>` images are any non-optional images defined in the `images`
array. Many parameters of this format can be provided for one `Template`.
`Template`s depending on these parameters will be processed only after the
`<component>` images are built.

| Example: | `registry.svc.ci.openshift.org/ci-op-<input-hash>/stable:<component>` |
| - | - |

#### `LOCAL_IMAGE_<pipeline-tag>`
The image repository URL from which the `<pipeline-tag>` image can be pulled.
If a component name has hyphens, replace them with underscores. For instance,
the `$LOCAL_IMAGE_SOME_NAME` parameter would point to the registry URL for
`some-name`. Valid `<pipeline-tag>` images are `src`, `bin`, `test-bin`, or
`rpms`. Many parameters of this format can be provided for one `Template`.
`Template`s depending on these parameters will be processed only after the
`<pipeline-tag>` images are built.

| Example: | `registry.svc.ci.openshift.org/ci-op-<input-hash>/pipeline:<pipeline-tag>` |
| - | - |

#### `JOB_NAME`
The job name as provided by Prow in the `$JOB_SPEC`.

#### `JOB_NAME_SAFE`
The `$JOB_NAME` transformed to be a valid Kubernetes object identifier.

#### `JOB_NAME_HASH`
A short hash of the `$JOB_NAME`. Append this to the `$NAMESPACE` to create an
identifier that is unique across all jobs for the same inputs. Note that this
identifier will not be unique across multiple builds of the same job for the
same inputs.

## Expected Output From `Template`s

The CI Operator attempts to determine the result of the `Template`'s execution
as well as assembling output artifacts.

### `Template` Execution Outcome

A `Template` definition is allowed to contain any number of `Pod`s. A `Template`
is considered successful if every `Container` in every `Pod` in the `Template`
definition starts at least once and terminates with a `0` exit code.

### `Template` Artifact Output

As with any job, use the `--artifact-dir` flag on the `ci-operator` command-line
to enable artifact gathering. When artifact gathering is enabled, the CI Operator
will add a `Container` named `artifacts` to any `Pod` defined in the `Template`
that exposes a volume named `artifacts`. All files that are added to the volume
will be uploaded to the appropriate GCS bucket for the job build that executed
the `Template`. Failures to retrieve or upload artifacts will not impact the
overall result of the job.