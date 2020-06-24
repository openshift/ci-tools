# Registry replacer

A small utility used to make sure that all builds use a cluster-local registry. It:

* Finds all ci-operator configs with at least one images directive
* Downloads the corresponding Dockerfile
* If it has a reference to the api.ci registry, updates the ci-operator config to replace that with a `base_image`
