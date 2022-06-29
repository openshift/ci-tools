# Updating Sippy configuration

This utility updates the Sippy configuration based on our job annotations and [release-controller configuration][release-controller].

Example invocation:

```
./sippy-config-generator --prow-jobs-dir ~/git/release/ci-operator/jobs --release-config ~/git/release/core-services/release-controller/_releases --customization-file ~/go/src/github.com/openshift/sippy/config/openshift-customizations.yaml
```

Commit the output to the sippy's repo config/openshift.yaml file.

## Customization

The customization file contains overrides or additional releases not
present (i.e., to create a pseudo-release of selected jobs).


Example:

```yaml
  prow:
    url: https://prow.ci.openshift.org/prowjobs.js
  releases:
    "Presubmits":
      regexp:
        - "^pull-ci-openshift-.*-(master|main)-e2e-.*"
    "4.11":
      jobs:
        aggregated-aws-ovn-upgrade-4.11-micro-release-openshift-release-analysis-aggregator: false
        periodic-ci-openshift-release-master-nightly-4.11-e2e-ovirt-csi: false
```
