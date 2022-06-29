# Updating Sippy configuration

This utility updates the Sippy configuration based on our job annotations and [release-controller configuration][release-controller].

Example invocation:

```
./sippy-config-generator --prow-jobs-dir ~/git/release/ci-operator/jobs --release-config ~/git/release/core-services/release-controller/_releases --customization-file ~/go/src/github.com/openshift/sippy/config/openshift-customizations.yaml
```

Commit the output to the sippy's repo config/openshift.yaml file.
