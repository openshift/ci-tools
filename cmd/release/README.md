# `release`

`release` is a command-line program that can be used to interact with and
extract various types of data from the `openshift/release` repository.

## Arguments

`release` expects to be executed from the root of the repository.  The
`-C`/`--root-dir` argument (similar to `git -C`) can be used to select another
path as the base for all other file paths.  The following parameters can be used
to change individual paths:

- `--config-dir`: `ci-operator` configuration files
- `--job-config`: Prow job configuration files
- `--registry`: step registry configuration files

## Commands

### `completion`

Automatic shell completion generation provided by `cobra`.  Available for
`bash`, `fish`, `powershell`, and `zsh`.

### `config`

Loads `ci-operator` configuration files and writes them to `stdout`, optionally
performing resolution.  The standard `ci-tools` loading process is used, meaning
a list of subdirectories can be passed as arguments to load a subset of files.

The `--resolve` argument can be used to fully expand tests in each configuration
using the contents of the step registry, as is done by `ci-operator` and
`ci-operator-configresolver` prior to test execution.

#### Examples

List all configuration files.

```console
$ release config --list | head -3
ci-operator/config/3scale/3scale-operator/3scale-3scale-operator-3scale-2.11-candidate.yaml
ci-operator/config/3scale/3scale-operator/3scale-3scale-operator-3scale-2.11-stable.yaml
ci-operator/config/3scale/3scale-operator/3scale-3scale-operator-3scale-2.12-candidate.yaml
```

```console
$ release config --list openshift/ci-tools
ci-operator/config/openshift/ci-tools/openshift-ci-tools-master.yaml
```

Dump all configuration files individually.

```console
$ release config openshift/ci-tools | head
---
base_images:
  cli:
    name: "4.10"
    namespace: ocp
    tag: cli
  golangci-lint:
    name: golangci-lint
    namespace: ci
    tag: v1.45.2
```

Count images/tests.

```console
$ release config | yq --slurp '[.[].images?|length]|add'
12560
$ release config | yq --slurp '[.[].tests?|length]|add'
30999
```

View resolved contents.

```console
$ release config openshift/origin/openshift-origin-master.yaml \
    | yq --yaml-output '.tests[2]'
as: e2e-aws
optional: true
steps:
  cluster_profile: aws-2
  env:
    BASE_DOMAIN: aws-2-.ci.openshift.org
  workflow: openshift-e2e-aws-loki
$ release config --resolve ci-operator/config/openshift/origin/openshift-origin-master.yaml \
    | yq --raw-output '.tests[2].literal_steps.pre[].as'
ipi-install-hosted-loki
ipi-conf
ipi-conf-aws
ipi-install-monitoringpvc
ipi-install-rbac
openshift-cluster-bot-rbac
ipi-install-install
ipi-install-times-collection
```

See every dependency override in tests.

```console
$ release config \
    | yq --compact-output '.tests[]?.steps?.dependencies?|select(.)' \
    | sort -u \
    | head
{"ASSISTED_AGENT_IMAGE":"pipeline:assisted-installer-agent","ASSISTED_CONTROLLER_IMAGE":"pipeline:assisted-installer-controller","ASSISTED_INSTALLER_IMAGE":"pipeline:assisted-installer","ASSISTED_SERVICE_IMAGE":"pipeline:assisted-service"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:candidate-4-11-multi","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:candidate","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:candidate"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","HYPERSHIFT_IMAGE":"hypershift-operator","INDEX_IMAGE":"assisted-service-operator-index","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","PROVIDER_IMAGE":"pipeline:cluster-api-provider-agent"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","HYPERSHIFT_IMAGE":"hypershift-operator","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","PROVIDER_IMAGE":"cluster-api-provider-agent"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:candidate-4-11"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:stable-4-10","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:stable-4-10","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:stable-4-10"}
{"ASSISTED_OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:stable-4-11","OPENSHIFT_INSTALL_RELEASE_IMAGE":"release:latest"}
```

### `job`

Loads Prow job configuration files and writes them to `stdout`.  The standard
`ci-tools` loading process is used, meaning a list of subdirectories can be
passed as arguments to load a subset of files.

#### Examples

List all configuration files.

```console
$ release job --list | head -3
ci-operator/jobs/3scale/3scale-operator/3scale-3scale-operator-3scale-2.11-candidate-presubmits.yaml
ci-operator/jobs/3scale/3scale-operator/3scale-3scale-operator-3scale-2.11-stable-presubmits.yaml
ci-operator/jobs/3scale/3scale-operator/3scale-3scale-operator-3scale-2.12-candidate-presubmits.yaml
```

Count by job type.

```console
$ release job | yq --slurp '[.[]|.periodics|length]|add'
2044
$ release job | yq --slurp '[.[]|.presubmits|values[]|length]|add'
35290
$ release job | yq --slurp '[.[]|.postsubmits|values[]|length]|add'
7208
```

View contents.

```console
$ release job --list openshift/ci-tools
ci-operator/jobs/openshift/ci-tools/openshift-ci-tools-master-periodics.yaml
ci-operator/jobs/openshift/ci-tools/openshift-ci-tools-master-postsubmits.yaml
ci-operator/jobs/openshift/ci-tools/openshift-ci-tools-master-presubmits.yaml
$ release job ci-operator/config/openshift/ci-tools | head
---
periodics:
- agent: kubernetes
  cluster: build02
  decorate: true
  extra_refs:
  - base_ref: master
    org: openshift
    repo: ci-tools
  interval: 5m
```

### `profile`

Dump cluster profiles supported by `ci-tools`.

#### Examples

```console
$ release profile | head
- cluster_type: aws
  lease_type: aws-quota-slice
  profile: aws
  secret: cluster-secrets-aws
- cluster_type: aws
  lease_type: aws-2-quota-slice
  profile: aws-2
  secret: cluster-secrets-aws-2
- cluster_type: aws
  lease_type: aws-3-quota-slice
```

### `registry`

Loads step registry components and writes them to `stdout`, optionally
performing resolution.  Without arguments, lists the names of all steps, chains,
and workflows in the registry.  Subcommands exist for each type, which can be
listed or examined.

#### Examples

Count by type.

```console
$ release registry step --list | wc -l
482
$ release registry chain --list | wc -l
338
$ release registry workflow --list | wc -l
418
```

```console
$ release registry | awk '{ ++s[$1] } END { for(k in s) print k, s[k] }'
    step 482
    workflow 418
    chain 338
```

View an element by type and name.

```console
$ release registry step openshift-e2e-test \
    | yq --raw-output '.env[]|[.name,.default]|join(": ")'
TEST_TYPE: suite
TEST_SUITE: openshift/conformance/parallel
TEST_SKIPS:
TEST_UPGRADE_OPTIONS:
TEST_REQUIRES_SSH:
TEST_INSTALL_CSI_DRIVERS:
TEST_CSI_DRIVER_MANIFEST:
$ release registry workflow openshift-e2e-aws-builds
---
env:
  TEST_SUITE: openshift/build
post:
- chain: ipi-aws-post
pre:
- chain: ipi-aws-pre
- ref: build-github-secrets
test:
- ref: openshift-e2e-test
```

Expand chains and workflows.

```console
$ release registry chain --tree ipi-aws-pre
chain: ipi-aws-pre
  chain: ipi-conf-aws
    step: ipi-conf
    step: ipi-conf-aws
    step: ipi-install-monitoringpvc
  chain: ipi-install
    step: ipi-install-rbac
    step: openshift-cluster-bot-rbac
    step: ipi-install-install
    step: ipi-install-times-collection
$ release registry workflow --tree openshift-e2e-aws-builds
workflow: openshift-e2e-aws-builds
pre:
  chain: ipi-aws-pre
    chain: ipi-conf-aws
      step: ipi-conf
      step: ipi-conf-aws
      step: ipi-install-monitoringpvc
    chain: ipi-install
      step: ipi-install-rbac
      step: openshift-cluster-bot-rbac
      step: ipi-install-install
      step: ipi-install-times-collection
  step: build-github-secrets
test:
  step: openshift-e2e-test
post:
  chain: ipi-aws-post
    step: gather-aws-console
    chain: ipi-deprovision
      chain: gather
        step: gather-must-gather
        step: gather-extra
        step: gather-audit-logs
      step: ipi-deprovision-deprovision
```

View resolved contents (note partial resolution).

```console
$ release registry workflow --resolve openshift-e2e-aws-builds \
    | yq --raw-output '.test[0].as'
openshift-e2e-test
$ release registry workflow --resolve openshift-e2e-aws-builds \
    | yq --raw-output '.test[0].env[]|[.name,.default]|join(": ")'
TEST_ARGS:
TEST_TYPE: suite
TEST_SUITE: openshift/build
TEST_UPGRADE_SUITE: all
TEST_SKIPS:
TEST_UPGRADE_OPTIONS:
TEST_REQUIRES_SSH:
TEST_INSTALL_CSI_DRIVERS:
TEST_CSI_DRIVER_MANIFEST:
TEST_IMAGE_MIRROR_REGISTRY:
```
