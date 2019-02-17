# Generated Prow jobs

## Presubmits

### Tests

For each
[test](https://github.com/openshift/ci-operator/blob/master/CONFIGURATION.md#tests)
specified in the configuration file, generate one presubmit running ci-operator
to build this test target.

```yaml
  - name: pull-ci-ORG-REPO-BRANCH-TEST
    branches:
    - BRANCH
    context: ci/prow/TEST
    rerun_command: /test TEST
    spec: <pod that runs `ci-operator --target=TEST`>
    trigger: ((?m)^/test( all| TEST),?(\s+|$))
    ...
```

### Images

If the configuration file does have a non-empty
[images](https://github.com/openshift/ci-operator/blob/master/CONFIGURATION.md#images)
array, generate a presubmit running ci-operator to build the `[images]` target.


```yaml
  - name: pull-ci-ORG-REPO-BRANCH-images
    branches:
    - BRANCH
    context: ci/prow/images
    rerun_command: /test images
    spec: <pod that runs `ci-operator --target=[images]`>
    trigger: ((?m)^/test( all| images),?(\s+|$))
    ...
```

### Hand-Edited Prow Configuration

If the existing Prow job configuration already exists, the generator will update it. The
following fields will not be overwritten if they are already present:

 - `always_run`
 - `run_if_changed`
 - `optional`
 - `max_concurrency`
 - `skip_report`

## Postsubmits

### Images

If the configuration file does have a non-empty
[images](https://github.com/openshift/ci-operator/blob/master/CONFIGURATION.md#images)
array, generate a postsubmit running ci-operator to built the `[images]`
target. The postsubmit also uses the `--promote` option of ci-operator to
promote the component images built by this postsubmit.

```yaml
  - name: branch-ci-ORG-REPO-BRANCH-images
    spec: <pod that runs `ci-operator --target=[images] --promote>
    ...
```

### Hand-Edited Prow Configuration

If the existing Prow job configuration already exists, the generator will update it. The
following fields will not be overwritten if they are already present:

 - `max_concurrency`